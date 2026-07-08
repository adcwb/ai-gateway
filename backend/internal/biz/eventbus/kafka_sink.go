package eventbus

import (
	"context"

	"github.com/segmentio/kafka-go"
)

// KafkaSink publishes each event to a topic named after its EventType
// ("audit"/"billing") — "topic per event type" per docs/design/09-
// extensibility.md "Event bus". Only constructed when the operator
// configures Kafka brokers (conf.Extensions.KafkaBrokers); zero overhead
// otherwise, matching every other optional integration in this codebase.
type KafkaSink struct {
	SinkName string
	writer   *kafka.Writer
}

func NewKafkaSink(name string, brokers []string) *KafkaSink {
	return &KafkaSink{
		SinkName: name,
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Balancer: &kafka.LeastBytes{},
		},
	}
}

func (s *KafkaSink) Name() string { return s.SinkName }

func (s *KafkaSink) Deliver(ctx context.Context, events []Event) error {
	msgs := make([]kafka.Message, len(events))
	for i, e := range events {
		msgs[i] = kafka.Message{Topic: e.EventType, Key: []byte(e.EventID), Value: e.Payload}
	}
	return s.writer.WriteMessages(ctx, msgs...)
}

// Close releases the underlying Kafka connection(s). Call once at shutdown.
func (s *KafkaSink) Close() error { return s.writer.Close() }
