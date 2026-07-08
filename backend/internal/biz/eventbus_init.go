package biz

import (
	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"

	"github.com/opscenter/ai-gateway/internal/biz/eventbus"
	"github.com/opscenter/ai-gateway/internal/conf"
)

// NewEventBus builds the on_audit/on_billing event bus (docs/design/09-
// extensibility.md "Event bus"), wiring in whichever sinks the operator has
// configured — none by default, matching every other optional integration
// in this codebase (ES, OTel): configuring nothing costs nothing extra.
func NewEventBus(db *gorm.DB, extConf *conf.Extensions, logger log.Logger) *eventbus.Bus {
	var sinks []eventbus.Sink
	if extConf != nil {
		if extConf.EventWebhookURL != "" {
			sinks = append(sinks, &eventbus.WebhookSink{
				SinkName: "webhook", URL: extConf.EventWebhookURL, HMACSecret: extConf.EventWebhookSecret,
				HTTPClient: newProxyClient(),
			})
		}
		if len(extConf.KafkaBrokers) > 0 {
			sinks = append(sinks, eventbus.NewKafkaSink("kafka", extConf.KafkaBrokers))
		}
	}
	return eventbus.NewBus(db, sinks, logger)
}
