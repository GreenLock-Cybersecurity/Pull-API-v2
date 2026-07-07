package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	pwd := "DemoStaff2026!"
	if len(os.Args) > 1 {
		pwd = os.Args[1]
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pwd), 10)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(h))
}
