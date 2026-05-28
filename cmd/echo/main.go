package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
)

type response struct {
	Name   string            `json:"name"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query"`
}

func main() {
	addr := flag.String("addr", ":9001", "listen address")
	name := flag.String("name", "echo", "service name")
	flag.Parse()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}

		payload := response{
			Name:   *name,
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  query,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})

	log.Printf("echo service %s listening on %s", *name, *addr)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
