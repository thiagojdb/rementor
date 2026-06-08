package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

type response struct {
	Service string `json:"service"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

func main() {
	servers := []struct {
		addr   string
		source string
		routes map[string]string
	}{
		{
			addr:   "127.0.0.1:18080",
			source: "remote",
			routes: map[string]string{
				"/":               "web",
				"/health":         "web",
				"/orders":         "orders-api",
				"/orders/health":  "orders-api",
				"/billing":        "billing-api",
				"/billing/health": "billing-api",
			},
		},
		{
			addr:   "127.0.0.1:28080",
			source: "local",
			routes: map[string]string{
				"/":       "web",
				"/health": "web",
			},
		},
		{
			addr:   "127.0.0.1:28081",
			source: "local",
			routes: map[string]string{
				"/orders":        "orders-api",
				"/orders/health": "orders-api",
			},
		},
		{
			addr:   "127.0.0.1:28082",
			source: "local",
			routes: map[string]string{
				"/billing":        "billing-api",
				"/billing/health": "billing-api",
			},
		},
	}

	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func(addr, source string, routes map[string]string) {
			defer wg.Done()
			mux := http.NewServeMux()
			for path, service := range routes {
				path, service := path, service
				mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(response{
						Service: service,
						Source:  source,
						Message: fmt.Sprintf("%s response from %s mock", service, source),
					})
				})
			}
			log.Printf("%s mock listening on http://%s", source, addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Printf("%s mock on %s stopped: %v", source, addr, err)
			}
		}(srv.addr, srv.source, srv.routes)
	}
	wg.Wait()
}
