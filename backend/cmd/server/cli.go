package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"gopkg.in/yaml.v3"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data"
	"github.com/opscenter/ai-gateway/internal/data/model"
	"github.com/opscenter/ai-gateway/internal/pkg"
)

// CLI subcommands (docs/design/10-deployment-and-ops.md):
//
//	server doctor -conf configs/config.yaml       # dependency & config checks
//	server rekey  -conf configs/config.yaml -old OLDKEY -new NEWKEY
//
// Returns true if a subcommand was dispatched (main should exit).
func runSubcommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "doctor":
		os.Exit(runDoctor(args[2:]))
	case "rekey":
		os.Exit(runRekey(args[2:]))
	}
	return false
}

func loadConfig(confPath string) (*conf.Bootstrap, error) {
	raw, err := os.ReadFile(confPath)
	if err != nil {
		return nil, err
	}
	var bc conf.Bootstrap
	if err := yaml.Unmarshal(raw, &bc); err != nil {
		return nil, err
	}
	bc.ApplyEnvOverrides()
	return &bc, nil
}

// runDoctor checks connectivity and configuration; the first thing support
// asks a user to run.
func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	confPath := fs.String("conf", "configs/config.yaml", "path to config file")
	fs.Parse(args)

	failures := 0
	check := func(name string, err error) {
		if err != nil {
			fmt.Printf("  ✗ %-18s %v\n", name, err)
			failures++
		} else {
			fmt.Printf("  ✓ %-18s ok\n", name)
		}
	}

	fmt.Println("ai-gateway doctor")
	bc, err := loadConfig(*confPath)
	check("config", err)
	if err != nil {
		return 1
	}

	if bc.System == nil || len(bc.System.EncryptionKey) != 32 {
		check("encryption_key", fmt.Errorf("must be exactly 32 bytes (got %d)", keyLen(bc)))
	} else {
		check("encryption_key", nil)
	}
	if bc.System == nil || bc.System.AdminToken == "" {
		check("admin_token", fmt.Errorf("empty — management API is OPEN (set AIGW_ADMIN_TOKEN in production)"))
	} else {
		check("admin_token", nil)
	}

	logger := log.NewStdLogger(os.Stderr)
	db, err := data.NewDB(bc.Database, logger)
	check("database", err)
	if err == nil {
		sqlDB, _ := db.DB()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		check("database ping", sqlDB.PingContext(ctx))
		cancel()
		sqlDB.Close()
	}

	rdb, err := data.NewRedis(bc.Redis, logger)
	check("redis", err)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		check("redis ping", rdb.Ping(ctx).Err())
		cancel()
		rdb.Close()
	}

	if failures > 0 {
		fmt.Printf("doctor: %d problem(s) found\n", failures)
		return 1
	}
	fmt.Println("doctor: all checks passed")
	return 0
}

func keyLen(bc *conf.Bootstrap) int {
	if bc.System == nil {
		return 0
	}
	return len(bc.System.EncryptionKey)
}

// runRekey re-encrypts stored secrets (virtual-key plaintexts + provider API
// keys) from the old encryption key to the new one, in a single transaction.
func runRekey(args []string) int {
	fs := flag.NewFlagSet("rekey", flag.ExitOnError)
	confPath := fs.String("conf", "configs/config.yaml", "path to config file")
	oldKey := fs.String("old", "", "current 32-byte encryption key")
	newKey := fs.String("new", "", "new 32-byte encryption key")
	fs.Parse(args)

	if len(*oldKey) != 32 || len(*newKey) != 32 {
		fmt.Println("rekey: -old and -new must both be exactly 32 bytes")
		return 1
	}
	bc, err := loadConfig(*confPath)
	if err != nil {
		fmt.Printf("rekey: load config: %v\n", err)
		return 1
	}
	logger := log.NewStdLogger(os.Stderr)
	db, err := data.NewDB(bc.Database, logger)
	if err != nil {
		fmt.Printf("rekey: connect database: %v\n", err)
		return 1
	}

	reKeys, reProviders := 0, 0
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var keys []model.AIVirtualKey
	if err := tx.Where("plain_key_encrypted <> ''").Find(&keys).Error; err != nil {
		tx.Rollback()
		fmt.Printf("rekey: load keys: %v\n", err)
		return 1
	}
	for i := range keys {
		plain, derr := pkg.DecryptAES(keys[i].PlainKeyEncrypted, []byte(*oldKey))
		if derr != nil {
			tx.Rollback()
			fmt.Printf("rekey: decrypt key id=%d failed (wrong -old key?): %v\n", keys[i].ID, derr)
			return 1
		}
		enc, eerr := pkg.EncryptAES(plain, []byte(*newKey))
		if eerr != nil {
			tx.Rollback()
			fmt.Printf("rekey: re-encrypt key id=%d: %v\n", keys[i].ID, eerr)
			return 1
		}
		if err := tx.Model(&model.AIVirtualKey{}).Where("id = ?", keys[i].ID).
			Update("plain_key_encrypted", enc).Error; err != nil {
			tx.Rollback()
			fmt.Printf("rekey: update key id=%d: %v\n", keys[i].ID, err)
			return 1
		}
		reKeys++
	}

	var providers []model.AIProvider
	if err := tx.Unscoped().Where("api_key <> ''").Find(&providers).Error; err != nil {
		tx.Rollback()
		fmt.Printf("rekey: load providers: %v\n", err)
		return 1
	}
	for i := range providers {
		plain, derr := pkg.DecryptAES(providers[i].APIKey, []byte(*oldKey))
		if derr != nil {
			tx.Rollback()
			fmt.Printf("rekey: decrypt provider id=%d failed (wrong -old key?): %v\n", providers[i].ID, derr)
			return 1
		}
		enc, eerr := pkg.EncryptAES(plain, []byte(*newKey))
		if eerr != nil {
			tx.Rollback()
			fmt.Printf("rekey: re-encrypt provider id=%d: %v\n", providers[i].ID, eerr)
			return 1
		}
		if err := tx.Unscoped().Model(&model.AIProvider{}).Where("id = ?", providers[i].ID).
			Update("api_key", enc).Error; err != nil {
			tx.Rollback()
			fmt.Printf("rekey: update provider id=%d: %v\n", providers[i].ID, err)
			return 1
		}
		reProviders++
	}

	if err := tx.Commit().Error; err != nil {
		fmt.Printf("rekey: commit: %v\n", err)
		return 1
	}
	fmt.Printf("rekey: done — %d virtual keys, %d providers re-encrypted.\n", reKeys, reProviders)
	fmt.Println("rekey: now update system.encryption_key (AIGW_ENCRYPTION_KEY) to the new value and restart all instances.")
	return 0
}
