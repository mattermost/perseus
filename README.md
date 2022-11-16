# Perseus

### What is Perseus

Perseus is a pgbouncer replacement written in pure Go. It has been written mainly to solve some unique scaling challenges we faced at Mattermost.

### How to deploy

1. We need a table called `perseus_auth` to be present in some database. This table will serve as the auth table to authenticate clients connecting to the service.
2. We need to procure a KMS Key ARN, and the corresponding AWS credentials to use that key.
3. We need rows in `perseus_auth` per DB to be present for each client connecting to the service.
4. Set the `schema_search_path` as a new query param in the MM DSN. This should be the same value as `source_schema` in the table.

Let's go through these steps in detail:
1. Create a table as per below:

```sql
CREATE TABLE perseus_auth (
    id serial PRIMARY KEY,
    source_db character varying(1024),
    source_schema character varying(64),
    source_user character varying(64),
    source_pass_hashed character varying(1024),
    dest_host character varying(64),
    dest_db character varying(1024),
    dest_pass_enc character varying(1024),
    dest_user character varying(64),
    UNIQUE (source_db, source_schema)
);
```

This table will have a row, for every source DB + dest DB combination. Now, `source_db`, `source_schema`, `source_user`, and `source_pass_hashed` are the client side values. And `dest_host`, `dest_db`, `dest_pass_enc`, and `dest_user` are the RDS side values that Perseus will use to connect to the DB.

2. Go the AWS console, and generate a KMS Key and note the ARN.

3. To get `source_pass_hashed`, run `go run ./cmd/genhash/ -password <passwd>`. This will give you the hashed password. To generate the password via code when generating the row from within a service (e.g. cloud-provisioner), use this code:

```go
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
	// Password is hashStr
}
```

To get the `dest_pass_enc`, run `go run ./cmd/encpass -access_key_id=<> -endpoint=<> -key_arn=<> -passwd=<> -region=<> -secret_access_key=<>` to get the encrypted password. Perseus will decrypt it and login to RDS. To generate the password via code when generating the row from within a service (e.g. cloud-provisioner), use this code:

```go
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
	// Password is: base64.StdEncoding.EncodeToString(enc.CiphertextBlob))
}
```

We have all the parts ready to populate the row in the DB!

4. Pretty self-explanatory. Just needs to be set on the MM side.

### Perseus Config

```
{
    "ListenAddress": ":5433",
    "AuthDBSettings": {
        // Additional query param settings to control pool size
        // pool_max_conns: integer greater than 0 (default 4)
        // pool_min_conns: integer 0 or greater (default 0)
        // pool_max_conn_lifetime: duration string (default 1hr)
        // pool_max_conn_idle_time: duration string (default 30mins)
        // pool_health_check_period: duration string (default 1min)
        // pool_max_conn_lifetime_jitter: duration string
        "AuthDBDSN": "postgres://mmuser:mostest@localhost:5432/loadtest?sslmode=disable&pool_min_conns=2",
        "AuthQueryTimeoutSecs": 2
    },
    "AWSSettings": {
        "AccessKeyId": "<>",
        "SecretAccessKey": "<>",
        "Region": "us-east-1",
        "Endpoint": "", // This can be left as blank
        "KMSKeyARN": "<>"
    },
    "PoolSettings": {
        // Set these settings to some reasonable values
        "MaxIdle":               3,
        "MaxOpen":               5,
        "MaxLifetimeSecs":       3600,
        "MaxIdletimeSecs":       300,
        "ConnCreateTimeoutSecs": 5,
        "ConnCloseTimeoutSecs":  1,
        "SchemaExecTimeoutSecs": 5 // This is the timeout which controls the time taken to execute the setting of the schema search path every time
        // we acquire a connection from the pool.
    }
}
```

### Reloading config

To reload its config, you can send a `SIGHUP` signal to the process. This will trigger Perseus to re-read the config.json file again and reload its configuration. Note that only pool settings can be reloaded at the moment without a restart. For changing other settings, they need a restart.

