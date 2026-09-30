// Package webui 内嵌 ainovel-server 的单页前端。
//
// 前端刻意不引入任何构建链：一个 index.html + 一份原生 JS，用 go:embed 打包进二进制，
// 服务启动后直接可用（无 node、无 CDN、无外网依赖）。它只消费 REST + SSE 两个契约。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var files embed.FS

// Handler 返回静态资源处理器。index.html 是所有未匹配路径的回退（SPA 语义）。
func Handler() http.Handler {
	sub, err := fs.Sub(files, "web")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r = r.Clone(r.Context())
			r.URL.Path = "/index.html"
		}
		fileServer.ServeHTTP(w, r)
	})
}
