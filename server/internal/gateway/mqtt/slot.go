package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/mqttspec"
	"github.com/taha2samy/quackquack/server/internal/sourcepipe"
)

const (
	workersPerSlot = 4
	workQueue      = 256
	dlqPayloadMax  = 4 << 10
)

// slot is this gateway's client for one slot of one connection.
type slot struct {
	t        *Transport
	key      slotKey
	connHash string
	cfg      atomic.Pointer[connConfig]
	cm       atomic.Pointer[autopaho.ConnectionManager]
	cancel   context.CancelFunc
	done     chan struct{}
	work     []chan paho.PublishReceived

	mu         sync.Mutex
	subscribed map[string]byte
	connected  bool
	lastErr    string
	reason     *int

	received, published, rejected atomic.Int64
}

func newSlot(t *Transport, k slotKey, c *connConfig) *slot {
	s := &slot{t: t, key: k, connHash: c.connHash, done: make(chan struct{}), subscribed: map[string]byte{}}
	s.cfg.Store(c)
	return s
}

func (s *slot) clientID() string {
	return fmt.Sprintf("%s-%d", s.cfg.Load().cfg.Connection.ClientIDPrefix, s.key.n)
}

// start connects in the background; autopaho reconnects until stop.
func (s *slot) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.work = make([]chan paho.PublishReceived, workersPerSlot)
	var wg sync.WaitGroup
	for i := range s.work {
		s.work[i] = make(chan paho.PublishReceived, workQueue)
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case pr := <-s.work[i]:
					s.handle(ctx, pr)
				}
			}
		})
	}
	go func() {
		defer close(s.done)
		defer wg.Wait()
		cc, err := s.clientConfig(ctx)
		if err != nil {
			s.setState(false, nil, err.Error())
			s.report(ctx)
			<-ctx.Done()
			return
		}
		cm, err := autopaho.NewConnection(ctx, cc)
		if err != nil {
			s.setState(false, nil, err.Error())
			s.report(ctx)
			<-ctx.Done()
			return
		}
		s.cm.Store(cm)
		<-ctx.Done()
		dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = cm.Disconnect(dctx) // the session stays on the broker for the next owner
		dcancel()
		metrics.MQTTConnected.DeleteLabelValues(s.key.conn, strconv.Itoa(s.key.n))
	}()
}

func (s *slot) stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
}

func (s *slot) setConfig(c *connConfig) {
	s.cfg.Store(c)
	if cm := s.cm.Load(); cm != nil {
		go s.syncSubscriptions(context.Background(), cm)
	}
}

func (s *slot) clientConfig(ctx context.Context) (autopaho.ClientConfig, error) {
	c := s.cfg.Load().cfg.Connection
	u, err := url.Parse(c.BrokerURL)
	if err != nil {
		return autopaho.ClientConfig{}, err
	}
	auth, err := mqttspec.ParseAuth(c.Auth)
	if err != nil {
		return autopaho.ClientConfig{}, fmt.Errorf("auth: %w", err)
	}
	tlsSpec, err := mqttspec.ParseTLS(c.TLS)
	if err != nil {
		return autopaho.ClientConfig{}, fmt.Errorf("tls: %w", err)
	}
	tlsCfg, err := buildTLS(tlsSpec, s.t.o.AllowInsecureTLS)
	if err != nil {
		return autopaho.ClientConfig{}, err
	}
	session := uint32(c.SessionExpiry)
	recvMax := uint16(min(c.ReceiveMaximum, 65535))
	cc := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		TlsCfg:                        tlsCfg,
		KeepAlive:                     uint16(c.Keepalive),
		CleanStartOnInitialConnection: false, // resume the slot's session (and its unacknowledged messages)
		SessionExpiryInterval:         session,
		ConnectTimeout:                10 * time.Second,
		ReconnectBackoff: func(attempt int) time.Duration {
			d := min(time.Second<<min(attempt, 5), 30*time.Second)
			return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
		},
		ConnectPacketBuilder: func(cp *paho.Connect, _ *url.URL) (*paho.Connect, error) {
			if cp.Properties == nil {
				cp.Properties = &paho.ConnectProperties{}
			}
			cp.Properties.ReceiveMaximum = &recvMax
			return cp, nil
		},
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			s.setState(true, nil, "")
			go s.report(ctx)
			go s.syncSubscriptionsFresh(ctx, cm)
		},
		OnConnectionDown: func() bool {
			s.setState(false, nil, "connection lost")
			go s.report(ctx)
			return true
		},
		OnConnectError: func(err error) {
			var ce *autopaho.ConnackError
			var code *int
			msg := err.Error()
			if errors.As(err, &ce) {
				n := int(ce.ReasonCode)
				code = &n
				msg = fmt.Sprintf("broker refused: 0x%02X %s", ce.ReasonCode, reasonName(ce.ReasonCode))
			}
			s.setState(false, code, msg)
			go s.report(ctx)
		},
		ClientConfig: paho.ClientConfig{
			ClientID:                   s.clientID(),
			EnableManualAcknowledgment: true,
			SendAcksInterval:           50 * time.Millisecond,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(pr paho.PublishReceived) (bool, error) {
				s.enqueue(ctx, pr)
				return true, nil
			}},
		},
	}
	switch auth.Method {
	case mqttspec.AuthPassword:
		cc.ConnectUsername = auth.Username
		if auth.Password != "" {
			pw, err := mqttspec.Resolve(auth.Password)
			if err != nil {
				return cc, err
			}
			cc.ConnectPassword = pw
		}
	case mqttspec.AuthMTLS:
		if tlsCfg == nil || len(tlsCfg.Certificates) == 0 {
			return cc, errors.New("mtls needs tls.cert and tls.key")
		}
	}
	return cc, nil
}

func buildTLS(t mqttspec.TLS, allowInsecure bool) (*tls.Config, error) {
	if t == (mqttspec.TLS{}) {
		return nil, nil // system roots for mqtts://
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName}
	if t.CA != "" {
		pem, err := mqttspec.Resolve(t.CA)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("tls.ca: no certificates in it")
		}
		cfg.RootCAs = pool
	}
	if t.Cert != "" {
		cert, err := mqttspec.Resolve(t.Cert)
		if err != nil {
			return nil, err
		}
		key, err := mqttspec.Resolve(t.Key)
		if err != nil {
			return nil, err
		}
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("tls client certificate: %v", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if t.InsecureSkipVerify {
		if !allowInsecure {
			return nil, errors.New("insecure_skip_verify needs QUACK_ALLOW_INSECURE_TLS=true")
		}
		cfg.InsecureSkipVerify = true
	}
	return cfg, nil
}

// syncSubscriptionsFresh subscribes everything after a (re)connect. With a
// resumed session the broker keeps old subscriptions; subscribing again is
// harmless and covers rules added while disconnected.
func (s *slot) syncSubscriptionsFresh(ctx context.Context, cm *autopaho.ConnectionManager) {
	s.mu.Lock()
	s.subscribed = map[string]byte{}
	s.mu.Unlock()
	s.syncSubscriptions(ctx, cm)
}

func (s *slot) syncSubscriptions(ctx context.Context, cm *autopaho.ConnectionManager) {
	c := s.cfg.Load()
	want := map[string]byte{}
	for f, q := range c.subscriptions() {
		if c.cfg.Connection.Replicas > 1 {
			f = "$share/" + c.cfg.Connection.ClientIDPrefix + "/" + f
		}
		want[f] = q
	}
	s.mu.Lock()
	var add []paho.SubscribeOptions
	var drop []string
	for f, q := range want {
		if cur, ok := s.subscribed[f]; !ok || cur != q {
			add = append(add, paho.SubscribeOptions{Topic: f, QoS: q})
		}
	}
	for f := range s.subscribed {
		if _, ok := want[f]; !ok {
			drop = append(drop, f)
		}
	}
	s.mu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if len(add) > 0 {
		// OnConnectionUp runs before the manager marks the connection usable:
		// wait for it, and retry a refused subscribe a few times.
		var err error
		for attempt := range 5 {
			if err = cm.AwaitConnection(sctx); err != nil {
				break
			}
			if _, err = cm.Subscribe(sctx, &paho.Subscribe{Subscriptions: add}); err == nil {
				break
			}
			select {
			case <-sctx.Done():
			case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
			}
		}
		if err != nil {
			s.setState(false, nil, "subscribe: "+err.Error())
			go s.report(ctx)
			return
		}
	}
	if len(drop) > 0 {
		_, _ = cm.Unsubscribe(sctx, &paho.Unsubscribe{Topics: drop})
	}
	s.mu.Lock()
	s.subscribed = want
	s.mu.Unlock()
}

// enqueue hands a message to a worker; messages of one topic always go to
// the same worker, so their order is kept.
func (s *slot) enqueue(ctx context.Context, pr paho.PublishReceived) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(pr.Packet.Topic))
	select {
	case s.work[h.Sum32()%uint32(len(s.work))] <- pr: // blocks when full: back-pressure on the broker
	case <-ctx.Done(): // not acknowledged: the broker redelivers to the next owner
	}
}

func (s *slot) handle(ctx context.Context, pr paho.PublishReceived) {
	c := s.cfg.Load()
	p := pr.Packet
	s.received.Add(1)
	metrics.MQTTMessages.WithLabelValues(s.key.conn).Inc()
	msg := sourcepipe.Message{Topic: p.Topic, Payload: p.Payload}
	if p.Properties != nil {
		msg.ContentType = p.Properties.ContentType
		if len(p.Properties.User) > 0 {
			msg.UserProperties = map[string]string{}
			for _, u := range p.Properties.User {
				msg.UserProperties[u.Key] = u.Value
			}
		}
	}
	// One counter per message: it is acknowledged when every value it
	// produced is durable on the bus (or rejected for good).
	var pending atomic.Int64
	pending.Add(1)
	var rmu sync.Mutex
	var reasons []string
	var device string
	addReason := func(r string) { rmu.Lock(); reasons = append(reasons, r); rmu.Unlock() }
	finish := func() {
		if pending.Add(-1) != 0 {
			return
		}
		metrics.MQTTUnacked.Dec()
		if len(reasons) > 0 {
			s.rejected.Add(1)
			s.deadLetter(ctx, c, "", p.Topic, device, p.Payload, reasons)
		}
		if p.QoS > 0 && pr.Client != nil {
			_ = pr.Client.Ack(p)
		}
	}
	metrics.MQTTUnacked.Inc()
	defer finish()

	matched := false
	for _, r := range c.rules {
		if !mqttspec.Match(r.filter, p.Topic) {
			continue
		}
		matched = true
		if r.captureUntil != nil && time.Now().Before(*r.captureUntil) {
			s.capture(ctx, c, r, msg)
		}
		start := time.Now()
		vals, rej := sourcepipe.Process(r.pipe, msg)
		metrics.MQTTDecoderSeconds.Observe(time.Since(start).Seconds())
		for _, x := range rej {
			addReason(x.Reason)
			if x.Fatal {
				metrics.MQTTRejected.WithLabelValues("pipeline").Inc()
			}
		}
		for _, v := range vals {
			device = v.DeviceExternalID
			devID, ok := c.grants[v.DeviceExternalID]
			if !ok {
				addReason(fmt.Sprintf("device %q is not granted to this connection", v.DeviceExternalID))
				metrics.MQTTRejected.WithLabelValues("not_granted").Inc()
				continue
			}
			pending.Add(1)
			err := s.t.o.Core.Publish(Publication{ConnectionID: s.key.conn, DeviceID: devID, Element: v.Element,
				Message: v.Message, TS: v.TS}, func(err error) {
				if err != nil {
					addReason("bus: " + err.Error())
					metrics.MQTTRejected.WithLabelValues("bus").Inc()
				} else {
					s.published.Add(1)
					metrics.MQTTValues.WithLabelValues(s.key.conn).Inc()
				}
				finish()
			})
			if err != nil {
				addReason(fmt.Sprintf("element %s: %v", v.Element, err))
				metrics.MQTTRejected.WithLabelValues("core").Inc()
				finish()
			}
		}
	}
	if !matched {
		addReason("no uplink rule matches this topic")
		metrics.MQTTRejected.WithLabelValues("no_rule").Inc()
	}
}

func (s *slot) deadLetter(ctx context.Context, c *connConfig, uplink, topic, device string, payload []byte, reasons []string) {
	rec := events.MQTTRejected{ConnectionID: c.cfg.Connection.ID, UplinkID: uplink, Topic: topic, DeviceExternalID: device,
		Payload: payload[:min(len(payload), dlqPayloadMax)], PayloadSize: len(payload), Reasons: reasons,
		GatewayID: s.t.o.GatewayID, Time: time.Now().UTC()}
	b, err := json.Marshal(rec)
	if err != nil || s.t.o.Records == nil {
		return
	}
	s.t.o.Records.PublishRecord(ctx, bus.RawRecord(events.TopicMQTTDLQ, c.cfg.Connection.ID, b))
}

func (s *slot) capture(ctx context.Context, c *connConfig, r *rule, m sourcepipe.Message) {
	rec := events.MQTTCaptured{ConnectionID: c.cfg.Connection.ID, UplinkID: r.id, Topic: m.Topic, Payload: m.Payload,
		ContentType: m.ContentType, UserProperties: m.UserProperties, GatewayID: s.t.o.GatewayID, Time: time.Now().UTC()}
	b, err := json.Marshal(rec)
	if err != nil || s.t.o.Records == nil {
		return
	}
	s.t.o.Records.PublishRecord(ctx, bus.RawRecord(events.TopicMQTTCapture, r.id, b))
}

func (s *slot) setState(connected bool, code *int, errMsg string) {
	s.mu.Lock()
	s.connected, s.reason = connected, code
	if errMsg != "" || connected {
		s.lastErr = errMsg
	}
	s.mu.Unlock()
	v := 0.0
	if connected {
		v = 1
	}
	metrics.MQTTConnected.WithLabelValues(s.key.conn, strconv.Itoa(s.key.n)).Set(v)
	if errMsg != "" {
		s.t.o.Log.Warn("mqtt: connection problem", "connection", s.key.conn, "slot", s.key.n, "err", errMsg)
	}
}

func (s *slot) report(ctx context.Context) {
	if s.t.o.Status == nil {
		return
	}
	s.mu.Lock()
	st := Status{ConnectionID: s.key.conn, Slot: s.key.n, Connected: s.connected, ReasonCode: s.reason, LastError: s.lastErr,
		Counters: map[string]int64{"received": s.received.Load(), "published": s.published.Load(), "rejected": s.rejected.Load()}}
	s.mu.Unlock()
	if errs := s.cfg.Load().errors; len(errs) > 0 && st.LastError == "" {
		st.LastError = errs[0]
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.t.o.Status.ReportStatus(cctx, st)
}

// reasonName names the MQTT 5 CONNACK reason codes operators meet most.
func reasonName(c byte) string {
	switch c {
	case 0x80:
		return "unspecified error"
	case 0x84:
		return "unsupported protocol version"
	case 0x85:
		return "client identifier not valid"
	case 0x86:
		return "bad user name or password"
	case 0x87:
		return "not authorized"
	case 0x88:
		return "server unavailable"
	case 0x89:
		return "server busy"
	case 0x8A:
		return "banned"
	case 0x8C:
		return "bad authentication method"
	case 0x95:
		return "packet too large"
	case 0x97:
		return "quota exceeded"
	case 0x9F:
		return "connection rate exceeded"
	}
	return ""
}
