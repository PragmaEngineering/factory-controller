package runtimehost

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

func startDatabase(root string) (func(), error) {
	credential := filepath.Join(root, "database-password")
	password, err := os.ReadFile(credential)
	if os.IsNotExist(err) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return nil, err
		}
		password = []byte(hex.EncodeToString(secret))
		err = os.WriteFile(credential, password, 0600)
	}
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := uint32(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	log, err := os.OpenFile(filepath.Join(root, "postgres.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	db := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().Port(port).
		Database("fabric").Password(string(password)).RuntimePath(filepath.Join(root, "postgres-runtime")).
		DataPath(filepath.Join(root, "postgres-data")).Logger(log).
		StartParameters(map[string]string{"listen_addresses": "127.0.0.1"}))
	if err = db.Start(); err != nil {
		log.Close()
		return nil, fmt.Errorf("start local PostgreSQL (see %s): %w", log.Name(), err)
	}
	previous, present := os.LookupEnv("FACTORY_DATABASE_URL")
	os.Setenv("FACTORY_DATABASE_URL", fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/fabric?sslmode=disable", password, port))
	return func() {
		_ = db.Stop()
		_ = log.Close()
		if present {
			os.Setenv("FACTORY_DATABASE_URL", previous)
		} else {
			os.Unsetenv("FACTORY_DATABASE_URL")
		}
	}, nil
}
