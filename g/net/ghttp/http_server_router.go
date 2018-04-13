// Copyright 2018 gf Author(https://gitee.com/johng/gf). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://gitee.com/johng/gf.
// 路由控制.

package ghttp

import (
    "errors"
    "strings"
    "container/list"
    "gitee.com/johng/gf/g/util/gregx"
)

// 解析pattern
func (s *Server)parsePatternForBindHandler(pattern string) (domain, method, uri string, err error) {
    uri    = pattern
    domain = gDEFAULT_DOMAIN
    method = gDEFAULT_METHOD
    if array, err := gregx.MatchString(`([a-zA-Z]+):(.+)`, pattern); len(array) > 1 && err == nil {
        method  = array[1]
        pattern = array[2]
    }
    if array, err := gregx.MatchString(`(.+)@([\w\.\-]+)`, pattern); len(array) > 1 && err == nil {
        uri     = array[1]
        domain  = array[2]
    }
    if uri == "" {
        err = errors.New("invalid pattern")
    }
    return
}

// 注册服务处理方法
func (s *Server) setHandler(pattern string, item *HandlerItem) error {
    domain, method, uri, err := s.parsePatternForBindHandler(pattern)
    if err != nil {
        return errors.New("invalid pattern")
    }
    item.uri    = uri
    item.domain = domain
    item.method = method
    // 静态注册
    s.hmmu.Lock()
    defer s.hmmu.Unlock()
    if method == gDEFAULT_METHOD {
        for v, _ := range s.methodsMap {
            s.handlerMap[s.handlerKey(domain, v, pattern)] = item
        }
    } else {
        s.handlerMap[s.handlerKey(domain, method, pattern)] = item
    }

    // 动态注册，首先需要判断是否是动态注册，如果不是那么就没必要添加到动态注册记录变量中
    // 非叶节点为哈希表检索节点，按照URI注册的层级进行高效检索，直至到叶子链表节点；
    // 叶子节点是链表，按照优先级进行排序，优先级高的排前面，按照遍历检索，按照哈希表层级检索后的叶子链表一般数据量不大，所以效率比较高；
    if s.isUriHasRule(uri) {
        s.htmu.Lock()
        defer s.htmu.Unlock()
        if _, ok := s.handlerTree[domain]; !ok {
            s.handlerTree[domain] = make(map[string]interface{})
        }
        p            := s.handlerTree[domain]
        array        := strings.Split(uri[1:], "/")
        item.priority = len(array)
        for _, v := range array {
            if len(v) == 0 {
                continue
            }
            switch v[0] {
                case ':':
                    fallthrough
                case '*':
                    v = "/"
                    fallthrough
                default:
                    if _, ok := p.(map[string]interface{})[v]; !ok {
                        p.(map[string]interface{})[v] = make(map[string]interface{})
                    }
                    p = p.(map[string]interface{})[v]

            }
        }
        // 到达叶子节点
        var l *list.List
        if v, ok := p.(map[string]interface{})["*list"]; !ok {
            l = list.New()
            p.(map[string]interface{})["*list"] = l
        } else {
            l = v.(*list.List)
        }
        //b,_ := gjson.New(s.handlerTree).ToJsonIndent()
        //fmt.Println(string(b))
        // 从头开始遍历链表，优先级高的放在前面
        for e := l.Front(); e != nil; e = e.Next() {
            if s.compareHandlerItemPriority(item, e.Value.(*HandlerItem)) {
                l.InsertBefore(item, e)
                return nil
            }
        }
        l.PushBack(item)
    }
    return nil
}

// 对比两个HandlerItem的优先级，需要非常注意的是，注意新老对比项的参数先后顺序
func (s *Server) compareHandlerItemPriority(newItem, oldItem *HandlerItem) bool {
    if newItem.priority > oldItem.priority {
        return true
    }
    if newItem.priority < oldItem.priority {
        return false
    }
    if strings.Count(newItem.uri, "/:") > strings.Count(oldItem.uri, "/:") {
        return true
    }
    return false
}

// 服务方法检索
func (s *Server) searchHandler(r *Request) *HandlerItem {
    s.hmmu.RLock()
    domains := []string{gDEFAULT_DOMAIN, strings.Split(r.Host, ":")[0]}
    // 首先进行静态匹配
    for _, domain := range domains {
        if f, ok := s.handlerMap[s.handlerKey(domain, r.Method, r.URL.Path)]; ok {
            s.hmmu.RUnlock()
            return f
        }
    }
    s.hmmu.RUnlock()
    // 其次进行动态匹配
    array := strings.Split(r.URL.Path[1:], "/")
    s.htmu.RLock()
    for _, domain := range domains {
        p, ok := s.handlerTree[domain]
        if !ok {
            continue
        }
        // 多层链表的目的是当叶子节点未有任何规则匹配时，让父级模糊匹配规则继续处理
        lists := make([]*list.List, 0)
        for k, v := range array {
            if _, ok := p.(map[string]interface{})["*list"]; ok {
                lists = append(lists, p.(map[string]interface{})["*list"].(*list.List))
            }
            if _, ok := p.(map[string]interface{})[v]; !ok {
                if _, ok := p.(map[string]interface{})["/"]; ok {
                    p = p.(map[string]interface{})["/"]
                    if k == len(array) - 1 {
                        if _, ok := p.(map[string]interface{})["*list"]; ok {
                            lists = append(lists, p.(map[string]interface{})["*list"].(*list.List))
                        }
                    }
                }
            } else {
                p = p.(map[string]interface{})[v]
                if k == len(array) - 1 {
                    if _, ok := p.(map[string]interface{})["*list"]; ok {
                        lists = append(lists, p.(map[string]interface{})["*list"].(*list.List))
                    }
                }
            }
        }

        // 多层链表遍历检索，从数组末尾的链表开始遍历，末尾的深度高优先级也高
        for i := len(lists) - 1; i >= 0; i-- {
            for e := lists[i].Front(); e != nil; e = e.Next() {
                item := e.Value.(*HandlerItem)
                if strings.EqualFold(item.method, gDEFAULT_METHOD) || strings.EqualFold(item.method, r.Method) {
                    regrule, names := s.patternToRegRule(item.uri)
                    if gregx.IsMatchString(regrule, r.URL.Path) {
                        // 如果需要query匹配，那么需要重新解析URL
                        if len(names) > 0 {
                            if match, err := gregx.MatchString(regrule, r.URL.Path); err == nil {
                                array := strings.Split(names, ",")
                                if len(match) > len(array) {
                                    for index, name := range array {
                                        r.values[name] = []string{match[index + 1]}
                                    }
                                }
                            }
                        }
                        s.htmu.RUnlock()
                        return item
                    }
                }
            }
        }
    }
    s.htmu.RUnlock()
    return nil
}

// 将pattern（不带method和domain）解析成正则表达式匹配以及对应的query字符串
func (s *Server) patternToRegRule(rule string) (regrule string, names string) {
    if len(rule) < 2 {
        return rule, ""
    }
    regrule = "^"
    array  := strings.Split(rule[1:], "/")
    for _, v := range array {
        if len(v) == 0 {
            continue
        }
        switch v[0] {
            case ':':
                regrule += `/([\w\.\-]+)`
                if len(names) > 0 {
                    names += ","
                }
                names += v[1:]
            case '*':
                regrule += `/(.*)`
                if len(names) > 0 {
                    names += ","
                }
                names += v[1:]
                return
            default:
                regrule += "/" + v
        }
    }
    regrule += `$`
    return
}

// 判断URI中是否包含动态注册规则
func (s *Server) isUriHasRule(uri string) bool {
    if len(uri) > 1 && (strings.Index(uri, "/:") != -1 || strings.Index(uri, "/*") != -1) {
        return true
    }
    return false
}

// 生成回调方法查询的Key
func (s *Server) handlerKey(domain, method, pattern string) string {
    return strings.ToUpper(method) + ":" + pattern + "@" + strings.ToLower(domain)
}

