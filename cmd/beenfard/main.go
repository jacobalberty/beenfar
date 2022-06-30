package main

import (
	"log"
	"net/http"

	"github.com/jacobalberty/beenfar/service/controller"
)

func main() {
	h := &controller.HttpHandler{}
	h.RegisterHandlers()

	log.Fatal(http.ListenAndServe(":8080", h))
}
