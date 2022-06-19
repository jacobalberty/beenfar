package main

import "github.com/jacobalberty/beenfar/controller"

func main() {
	h := &controller.HttpHandler{}
	h.RegisterHandlers()
}
