package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func main() {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(key))
}
