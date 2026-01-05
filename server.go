package main

import (
	"log"
	"net/http"
)

func main() {
	log.Println("Starting CRT Weather Terminal on :8000")
	http.Handle("/", http.FileServer(http.Dir(".")))
	log.Fatal(http.ListenAndServe(":8000", nil))
}
