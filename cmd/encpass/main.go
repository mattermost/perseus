package main

import (
	"encoding/base64"
	"flag"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
)

func main() {
	var accessKeyID, secretAccessKey, region, endpoint, keyARN, pass string
	flag.StringVar(&accessKeyID, "access_key_id", "", "Access Key Id")
	flag.StringVar(&secretAccessKey, "secret_access_key", "", "Secret Access Key")
	flag.StringVar(&region, "region", "", "Region")
	flag.StringVar(&endpoint, "endpoint", "", "Endpoint")
	flag.StringVar(&keyARN, "key_arn", "", "KMS Key ARN")
	flag.StringVar(&pass, "passwd", "", "Password")
	flag.Parse()

	creds := credentials.NewStaticCredentials(accessKeyID, secretAccessKey, "")
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(region),
		Endpoint:    aws.String(endpoint),
		Credentials: creds,
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	svc := kms.New(sess)
	enc, err := svc.Encrypt(&kms.EncryptInput{
		KeyId:     aws.String(keyARN),
		Plaintext: []byte(pass),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("Your password is %q (without the quotes)\n", base64.StdEncoding.EncodeToString(enc.CiphertextBlob))
}
