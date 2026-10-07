// Package bus wraps franz-go for the three Redpanda topics.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/events"
)

type TopicSpec struct {
	Name       string
	Partitions int32
	Configs    map[string]*string
}

func ptr(s string) *string { return &s }

// Topics are the only topics the system uses (never one per element/device).
var Topics = []TopicSpec{
	{Name: events.TopicElementEvents, Partitions: 12, Configs: map[string]*string{"retention.ms": ptr("604800000")}},
	{Name: events.TopicControlEvents, Partitions: 3, Configs: map[string]*string{"retention.ms": ptr("604800000")}},
	{Name: events.TopicPresence, Partitions: 3, Configs: map[string]*string{"cleanup.policy": ptr("compact")}},
	{Name: events.TopicElementState, Partitions: 12, Configs: map[string]*string{"cleanup.policy": ptr("compact")}},
	{Name: events.TopicElementEventsDLQ, Partitions: 3, Configs: map[string]*string{"retention.ms": ptr("2592000000")}}, // 30 days
}

// EnsureTopics creates missing topics. Existing topics are left untouched.
func EnsureTopics(ctx context.Context, brokers []string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	for _, t := range Topics {
		resp, err := adm.CreateTopic(ctx, t.Partitions, -1, t.Configs, t.Name)
		if err == nil {
			err = resp.Err
		}
		if err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("bus: create topic %s: %w", t.Name, err)
		}
	}
	return nil
}

// Producer publishes CloudEvents in structured mode.
type Producer struct {
	cl  *kgo.Client
	log *slog.Logger
	// OnError is called for async produce failures (metrics hook).
	OnError func(topic string, err error)
}

func NewProducer(brokers []string, acksAll bool, log *slog.Logger) (*Producer, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ProducerLinger(5 * time.Millisecond),
		kgo.ProducerBatchCompression(kgo.Lz4Compression()),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		// Bounded: if Redpanda is down, records fail after the timeout and
		// TryProduce refuses new ones once the buffer is full, instead of
		// blocking every socket's read loop (and buffering gigabytes).
		kgo.RecordDeliveryTimeout(30 * time.Second),
		kgo.MaxBufferedBytes(64 << 20),
	}
	if acksAll {
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	} else {
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Producer{cl: cl, log: log}, nil
}

func Record(topic string, ev *events.Event) (*kgo.Record, error) {
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return RawRecord(topic, ev.PartitionKey, b), nil
}

func RawRecord(topic, key string, value []byte) *kgo.Record {
	return &kgo.Record{
		Topic:   topic,
		Key:     []byte(key),
		Value:   value,
		Headers: []kgo.RecordHeader{{Key: "content-type", Value: []byte(events.ContentTypeHeader)}},
	}
}

// Publish sends asynchronously and never blocks; failures (including a full
// buffer while Redpanda is unreachable) are logged and reported to OnError.
func (p *Producer) Publish(ctx context.Context, topic string, ev *events.Event) {
	rec, err := Record(topic, ev)
	if err != nil {
		p.log.Error("bus: encode event", "err", err)
		return
	}
	p.PublishRecord(ctx, rec)
}

// PublishRecord is Publish for a prepared record.
func (p *Producer) PublishRecord(ctx context.Context, rec *kgo.Record) {
	topic := rec.Topic
	p.cl.TryProduce(ctx, rec, func(_ *kgo.Record, err error) {
		if err != nil {
			p.log.Warn("bus: produce failed", "topic", topic, "err", err)
			if p.OnError != nil {
				p.OnError(topic, err)
			}
		}
	})
}

// PublishSync sends records and waits for all acks.
func (p *Producer) PublishSync(ctx context.Context, recs ...*kgo.Record) error {
	return p.cl.ProduceSync(ctx, recs...).FirstErr()
}

func (p *Producer) Close() {
	_ = p.cl.Flush(context.Background())
	p.cl.Close()
}

// Handler processes one decoded event from a topic.
type Handler func(ctx context.Context, topic string, ev *events.Event)

// Broadcast consumes every partition of the given topics from the current end,
// without a consumer group, so every gateway instance sees every event.
func Broadcast(ctx context.Context, brokers []string, topics []string, log *slog.Logger, h Handler) error {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
		kgo.FetchMaxWait(100*time.Millisecond),
	)
	if err != nil {
		return err
	}
	defer cl.Close()
	for {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(t string, p int32, err error) {
			log.Warn("bus: fetch error", "topic", t, "partition", p, "err", err)
		})
		fetches.EachRecord(func(r *kgo.Record) {
			var ev events.Event
			if err := json.Unmarshal(r.Value, &ev); err != nil {
				log.Warn("bus: undecodable record", "topic", r.Topic, "offset", r.Offset, "err", err)
				return
			}
			h(ctx, r.Topic, &ev)
		})
	}
}

// GroupConsumer consumes with a shared consumer group and manual commits.
type GroupConsumer struct {
	cl *kgo.Client
}

func NewGroupConsumer(brokers []string, group string, topics ...string) (*GroupConsumer, error) {
	cl, err := kgo.NewClient(debugLogger(),
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(200*time.Millisecond),
	)
	if err != nil {
		return nil, err
	}
	return &GroupConsumer{cl: cl}, nil
}

// Run polls batches, calls process, and commits only after process succeeds.
// On failure the batch is retried after a pause (at-least-once).
func (g *GroupConsumer) Run(ctx context.Context, log *slog.Logger, maxRecords int, process func(context.Context, []*kgo.Record) error) error {
	defer g.cl.Close()
	for {
		fetches := g.cl.PollRecords(ctx, maxRecords)
		if ctx.Err() != nil {
			g.cl.AllowRebalance()
			return nil
		}
		fetches.EachError(func(t string, p int32, err error) {
			log.Warn("bus: fetch error", "topic", t, "partition", p, "err", err)
		})
		recs := fetches.Records()
		log.Debug("bus: polled", "records", len(recs))
		if len(recs) == 0 {
			g.cl.AllowRebalance()
			continue
		}
		for {
			err := process(ctx, recs)
			if err == nil {
				break
			}
			log.Error("bus: batch failed, retrying", "records", len(recs), "err", err)
			select {
			case <-ctx.Done():
				g.cl.AllowRebalance()
				return nil
			case <-time.After(2 * time.Second):
			}
		}
		if err := g.cl.CommitRecords(ctx, recs...); err != nil {
			log.Warn("bus: commit failed", "err", err)
		}
		g.cl.AllowRebalance()
	}
}

// ReadAll calls fn for every record of a (compacted) topic, from the start up
// to the end offsets at the time of the call, then returns.
func ReadAll(ctx context.Context, brokers []string, topic string, fn func(*kgo.Record)) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	ends, err := kadm.NewClient(cl).ListEndOffsets(ctx, topic)
	if err != nil {
		return err
	}
	if err := ends.Error(); err != nil {
		return err
	}
	starts := map[string]map[int32]kgo.Offset{topic: {}}
	pending := map[int32]int64{}
	ends.Each(func(o kadm.ListedOffset) {
		if o.Offset > 0 {
			starts[topic][o.Partition] = kgo.NewOffset().AtStart()
			pending[o.Partition] = o.Offset
		}
	})
	if len(pending) == 0 {
		return nil
	}
	rd, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(starts), kgo.FetchMaxWait(200*time.Millisecond))
	if err != nil {
		return err
	}
	defer rd.Close()
	for len(pending) > 0 {
		fetches := rd.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		var ferr error
		fetches.EachError(func(_ string, _ int32, err error) { ferr = err })
		if ferr != nil {
			return ferr
		}
		fetches.EachRecord(func(r *kgo.Record) {
			if end, ok := pending[r.Partition]; ok {
				fn(r)
				if r.Offset+1 >= end {
					delete(pending, r.Partition)
				}
			}
		})
		// compaction can leave the last offsets empty: stop at the high watermark
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			if end, ok := pending[p.Partition]; ok && p.HighWatermark >= end && len(p.Records) == 0 {
				delete(pending, p.Partition)
			}
		})
	}
	return nil
}

// debugLogger enables franz-go client logs when QUACK_KAFKA_DEBUG is set.
func debugLogger() kgo.Opt {
	if os.Getenv("QUACK_KAFKA_DEBUG") == "" {
		return kgo.WithLogger(nil)
	}
	return kgo.WithLogger(kgo.BasicLogger(os.Stderr, kgo.LogLevelInfo, nil))
}
