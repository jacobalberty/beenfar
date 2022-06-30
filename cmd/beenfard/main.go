package main

import "github.com/jacobalberty/beenfar/service/controller"

func main() {
	h := &controller.HttpHandler{}
	h.RegisterHandlers()
}
