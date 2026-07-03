package main

import (
	"flag"
	"os"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"gopkg.in/yaml.v3"

	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/server"
)

var flagconf string

func init() {
	flag.StringVar(&flagconf, "conf", "configs/config.yaml", "path to config file")
}

func main() {
	flag.Parse()

	logger := log.NewStdLogger(os.Stdout)
	helper := log.NewHelper(logger)

	data, err := os.ReadFile(flagconf)
	if err != nil {
		helper.Fatalf("read config %s: %v", flagconf, err)
	}

	var bc conf.Bootstrap
	if err := yaml.Unmarshal(data, &bc); err != nil {
		helper.Fatalf("unmarshal config: %v", err)
	}
	bc.ApplyEnvOverrides()

	if bc.System == nil || len(bc.System.EncryptionKey) != 32 {
		helper.Warn("system.encryption_key 不是 32 字节：AES-256 将使用零填充/截断后的密钥，生产环境请设置精确 32 字节密钥")
	}

	app, cleanup, err := wireApp(&bc, logger)
	if err != nil {
		helper.Fatalf("init app: %v", err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		helper.Fatalf("run app: %v", err)
	}
}

func newApp(logger log.Logger, srv *server.KratosServer) *kratos.App {
	return kratos.New(
		kratos.Name("ai-gateway"),
		kratos.Version("1.0.0"),
		kratos.Logger(logger),
		kratos.Server(srv),
	)
}
