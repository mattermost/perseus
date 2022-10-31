package main

import (
	"encoding/base64"
	"flag"
	"fmt"

	scrypt "github.com/agnivade/easy-scrypt"
)

func main() {
	var pass string
	flag.StringVar(&pass, "password", "test", "Password to hash.")
	flag.Parse()

	hashBytes, err := scrypt.DerivePassphrase(pass, 32)
	if err != nil {
		fmt.Println(err)
		return
	}

	hashStr := base64.StdEncoding.EncodeToString(hashBytes)
	fmt.Printf("Your hash is %q (without the quotes)\n", hashStr)
}
