package main

import (
	"log"
	"net/http"

	"github.com/jacobalberty/beenfar/service"
)

func main() {

	bfs := service.NewBeenFarService()
	bfs.Init()

	log.Fatal(http.ListenAndServe(":8080", bfs))
}
