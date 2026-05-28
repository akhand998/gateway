package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	secret := flag.String("secret", "dev-secret", "HMAC secret for signing")
	tenantID := flag.String("tenant", "tenant-a", "tenant ID to embed in token")
	expiresIn := flag.Duration("expires-in", time.Hour, "token validity duration")
	flag.Parse()

	expiration := time.Now().Add(*expiresIn)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": *tenantID,
		"exp":       expiration.Unix(),
		"iat":       time.Now().Unix(),
	})

	signed, err := token.SignedString([]byte(*secret))
	if err != nil {
		log.Fatalf("failed to sign token: %v", err)
	}

	fmt.Println(signed)
}
