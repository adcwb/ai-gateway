//go:build wireinject
// +build wireinject

// The build tag makes sure the stub is not built with the production binary.
// Run `wire` to regenerate wire_gen.go.

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"

	"github.com/opscenter/ai-gateway/internal/biz"
	"github.com/opscenter/ai-gateway/internal/conf"
	"github.com/opscenter/ai-gateway/internal/data"
	"github.com/opscenter/ai-gateway/internal/observability"
	"github.com/opscenter/ai-gateway/internal/server"
	"github.com/opscenter/ai-gateway/internal/service"
)

func wireApp(bc *conf.Bootstrap, logger log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		provideDatabase,
		provideRedis,
		provideAI,
		provideSystem,
		provideServer,
		observability.NewMetrics,
		data.ProviderSet,
		biz.ProviderSet,
		service.NewGatewayService,
		server.ProviderSet,
		newApp,
	))
}
