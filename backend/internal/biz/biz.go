package biz

import "github.com/google/wire"

// ProviderSet is biz providers.
var ProviderSet = wire.NewSet(
	NewGatewayUseCase,
	NewQuotaManager,
	NewAuditWorker,
	NewRouterManager,
	NewBillingManager,
)
