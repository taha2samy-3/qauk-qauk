// Package bus wraps franz-go for the three Redpanda topics.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	// Tombstones of deleted devices are kept a day so slow readers still see them.
	{Name: events.TopicDeviceConfig, Partitions: 3, Configs: map[string]*string{"cleanup.policy": ptr("compact"), "delete.retention.ms": ptr("86400000")}},
	{Name: events.TopicMQTTConfig, Partitions: 3, Configs: map[string]*string{"cleanup.policy": ptr("compact"), "delete.retention.ms": ptr("86400000")}},
	{Name: events.TopicMQTTDLQ, Partitions: 3, Configs: map[string]*string{"retention.ms": ptr("2592000000")}},            // 30 days
	{Name: events.TopicElementPipelineDLQ, Partitions: 3, Configs: map[string]*string{"retention.ms": ptr("2592000000")}}, // 30 days
	{Name: events.TopicMQTTCapture, Partitions: 3, Configs: map[string]*string{"retention.ms": ptr("3600000")}},           // 1 hour
}

// topicPrefix namespaces every topic name on the cluster (QUACK_TOPIC_PREFIX),
// so environments (or test runs) that share a Redpanda cluster can't see each
// other's events or device configs. Code uses the logical names from the
// events package; this package maps them to the names on the cluster.
var topicPrefix string

// SetTopicPrefix sets the topic namespace for this process. Call it once at
// startup, before any producer or consumer is created.
func SetTopicPrefix(p string) { topicPrefix = p }

// TopicName returns the name of a logical topic on the cluster.
func TopicName(logical string) string { return topicPrefix + logical }

func logicalTopic(name string) string { return strings.TrimPrefix(name, topicPrefix) }

func topicNames(logical []string) []string {
	out := make([]string, len(logical))
	for i, t := range logical {
		out[i] = TopicName(t)
	}
	return out
}

// DeleteTopics removes this namespace's topics and waits until they are gone
// (tests use it to start from empty topics).
func DeleteTopics(ctx context.Context, brokers []string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	names := make([]string, len(Topics))
	for i, t := range Topics {
		names[i] = TopicName(t.Name)
	}
	if _, err := adm.DeleteTopics(ctx, names...); err != nil {
		return err
	}
	for {
		listed, err := adm.ListTopics(ctx, names...)
		if err != nil {
			return err
		}
		left := 0
		for _, d := range listed {
			if d.Err == nil {
				left++
			}
		}
		if left == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
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
		resp, err := adm.CreateTopic(ctx, t.Partitions, -1, t.Configs, TopicName(t.Name))
		if err == nil {
			err = resp.Err
		}
		if err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("bus: create topic %s: %w", TopicName(t.Name), err)
		}
	}
	return nil
}

// Producer publishes CloudEvents in structured mode.
const (
	maxBufferedBytes   = 64 << 20
	maxBufferedRecords = 200_000
	// Over these, Publish waits (up to backpressureWait) for the buffer to
	// drain before handing over the record: bursts slow the publisher (a
	// device's read loop, a REST request) instead of losing messages. Only
	// when Redpanda is unreachable for longer does the buffer fill and
	// records get dropped (counted by OnError).
	backpressureBytes   = maxBufferedBytes * 3 / 4
	backpressureRecords = maxBufferedRecords * 3 / 4
	backpressureWait    = 2 * time.Second
)

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
		kgo.MaxBufferedBytes(maxBufferedBytes),
		kgo.MaxBufferedRecords(maxBufferedRecords),
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
	if value == nil { // tombstone (compacted topics)
		return &kgo.Record{Topic: TopicName(topic), Key: []byte(key)}
	}
	return &kgo.Record{
		Topic:   TopicName(topic),
		Key:     []byte(key),
		Value:   value,
		Headers: []kgo.RecordHeader{{Key: "content-type", Value: []byte(events.ContentTypeHeader)}},
	}
}

// Publish sends asynchronously. It only waits while the produce buffer is
// nearly full (backpressure, at most backpressureWait); failures (including a
// full buffer while Redpanda is unreachable) are logged and reported to OnError.
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
	p.waitForRoom(ctx)
	topic := logicalTopic(rec.Topic)
	p.cl.TryProduce(ctx, rec, func(_ *kgo.Record, err error) {
		if err != nil {
			p.log.Warn("bus: produce failed", "topic", topic, "err", err)
			if p.OnError != nil {
				p.OnError(topic, err)
			}
		}
	})
}

// PublishDurable is Publish with a callback once the record is acknowledged
// by the broker (acks=all by default) or failed. Transports that must not
// lose data (MQTT QoS 1) acknowledge their source only then.
func (p *Producer) PublishDurable(ctx context.Context, topic string, ev *events.Event, done func(error)) {
	rec, err := Record(topic, ev)
	if err != nil {
		done(err)
		return
	}
	p.waitForRoom(ctx)
	p.cl.TryProduce(ctx, rec, func(_ *kgo.Record, err error) {
		if err != nil && p.OnError != nil {
			p.OnError(topic, err)
		}
		done(err)
	})
}

// waitForRoom applies backpressure while the produce buffer is nearly full.
func (p *Producer) waitForRoom(ctx context.Context) {
	full := func() bool {
		return p.cl.BufferedProduceRecords() >= backpressureRecords || p.cl.BufferedProduceBytes() >= backpressureBytes
	}
	if !full() {
		return
	}
	deadline := time.Now().Add(backpressureWait)
	for full() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
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
		kgo.ConsumeTopics(topicNames(topics)...),
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
			h(ctx, logicalTopic(r.Topic), &ev)
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
		kgo.ConsumeTopics(topicNames(topics)...),
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
	topic = TopicName(topic)
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

// Follow reads a (compacted) topic from the start and keeps following it
// until ctx ends, calling fn for every record (tombstones have a nil Value).
// caughtUp is called once, when the records that existed at start have been
// read, or after catchUpTimeout so a slow topic can't block startup forever.
func Follow(ctx context.Context, brokers []string, topic string, log *slog.Logger, fn func(*kgo.Record), caughtUp func()) error {
	const catchUpTimeout = 30 * time.Second
	topic = TopicName(topic)
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchMaxWait(200*time.Millisecond),
	)
	if err != nil {
		return err
	}
	defer cl.Close()
	pending := map[int32]int64{}
	ends, err := kadm.NewClient(cl).ListEndOffsets(ctx, topic)
	if err == nil {
		err = ends.Error()
	}
	if err != nil {
		return err
	}
	ends.Each(func(o kadm.ListedOffset) {
		if o.Offset > 0 {
			pending[o.Partition] = o.Offset
		}
	})
	done := false
	check := func() {
		if !done && len(pending) == 0 {
			done = true
			caughtUp()
		}
	}
	check()
	deadline := time.Now().Add(catchUpTimeout)
	for {
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		fetches := cl.PollFetches(pctx)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(t string, p int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				log.Warn("bus: fetch error", "topic", t, "partition", p, "err", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			fn(r)
			if end, ok := pending[r.Partition]; ok && r.Offset+1 >= end {
				delete(pending, r.Partition)
			}
		})
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			if end, ok := pending[p.Partition]; ok && p.HighWatermark >= end && len(p.Records) == 0 && p.Err == nil {
				delete(pending, p.Partition)
			}
		})
		if !done && time.Now().After(deadline) {
			log.Warn("bus: catch-up timed out, continuing", "topic", topic, "partitions_pending", len(pending))
			clear(pending)
		}
		check()
	}
}

// ReadTail returns up to n of the newest records of each partition of a
// topic (oldest first per partition), for admin views of DLQ-style topics.
func ReadTail(ctx context.Context, brokers []string, topic string, n int64) ([]*kgo.Record, error) {
	topic = TopicName(topic)
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ends, err := adm.ListEndOffsets(ctx, topic)
	if err == nil {
		err = ends.Error()
	}
	if err != nil {
		return nil, err
	}
	starts, err := adm.ListStartOffsets(ctx, topic)
	if err == nil {
		err = starts.Error()
	}
	if err != nil {
		return nil, err
	}
	from := map[string]map[int32]kgo.Offset{topic: {}}
	pending := map[int32]int64{}
	ends.Each(func(o kadm.ListedOffset) {
		start := o.Offset - n
		if s, ok := starts.Lookup(topic, o.Partition); ok && s.Offset > start {
			start = s.Offset
		}
		if start < o.Offset {
			from[topic][o.Partition] = kgo.NewOffset().At(start)
			pending[o.Partition] = o.Offset
		}
	})
	if len(pending) == 0 {
		return nil, nil
	}
	rd, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(from), kgo.FetchMaxWait(200*time.Millisecond))
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	var out []*kgo.Record
	for len(pending) > 0 {
		fetches := rd.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return out, err
		}
		fetches.EachRecord(func(r *kgo.Record) {
			if end, ok := pending[r.Partition]; ok {
				out = append(out, r)
				if r.Offset+1 >= end {
					delete(pending, r.Partition)
				}
			}
		})
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			if end, ok := pending[p.Partition]; ok && p.HighWatermark >= end && len(p.Records) == 0 && p.Err == nil {
				delete(pending, p.Partition)
			}
		})
	}
	return out, nil
}
