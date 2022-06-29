package main

import (
	"io"
	"log"
	"os"

	"github.com/jacobalberty/beenfar/util"
)

func main() {
	if len(os.Args) == 1 {
		log.Println("You must provide at least one argument")
		return
	}

	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Printf("Error reading file '%s': %s", os.Args[1], err)
		return
	}

	ipd, err := util.NewInformPD(b)
	if err != nil {
		log.Printf("Error decoding packet: %s", err)
		return
	}

	json, err := ipd.Uncompress()
	if err != nil {
		log.Fatalf("Error decompressing inform packet: %v", err)
	}

	_, err = io.Copy(os.Stdout, json)
	if err != nil {
		log.Fatalf("Error outputing json to terminal: %v", err)
	}
}
