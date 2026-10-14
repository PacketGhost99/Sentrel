package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	pb "sentrel/gen/sentrelv1"
	"sentrel/internal/sigs"
)

// simAgent is one simulated agent: it holds a stream open and reports on an
// interval, reconnecting with backoff like the real agent.
type simAgent struct {
	cfg  simConfig
	id   *identity
	idx  int
	st   *stats
	rng  *mrand.Rand
	cert tls.Certificate

	sendMu sync.Mutex // gRPC streams allow one sender at a time
	vals   map[string]float64
}

func newSimAgent(cfg simConfig, id *identity, idx int, st *stats) *simAgent {
	a := &simAgent{cfg: cfg, id: id, idx: idx, st: st, rng: mrand.New(mrand.NewPCG(uint64(idx)+1, 42)), vals: map[string]float64{}}
	if c, err := tls.X509KeyPair([]byte(id.CertPEM), []byte(id.KeyPEM)); err == nil {
		a.cert = c
	}
	return a
}

func (a *simAgent) run(ctx context.Context) {
	backoff := 2 * time.Second
	first := true
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx, first)
		first = false
		if ctx.Err() != nil {
			return
		}
		a.st.reconnects.Add(1)
		a.cfg.log.Debug("disconnected", "agent", a.cfg.hostname(a.idx), "err", err)
		if time.Since(start) > time.Minute {
			backoff = 2 * time.Second
		}
		wait := backoff + time.Duration(a.rng.Int64N(int64(backoff/2)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = time.Duration(math.Min(float64(backoff*2), float64(60*time.Second)))
	}
}

func (a *simAgent) send(stream pb.AgentService_ConnectClient, m *pb.AgentMessage) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	if err := stream.Send(m); err != nil {
		a.st.sendErrors.Add(1)
		return err
	}
	a.st.bytesSent.Add(int64(proto.Size(m)))
	return nil
}

func (a *simAgent) hello() (*pb.AgentMessage, error) {
	ms := time.Now().UnixMilli()
	h := sha256.Sum256(sigs.HelloMessage(a.id.AgentID, ms))
	sig, err := ecdsa.SignASN1(rand.Reader, a.id.key, h[:])
	if err != nil {
		return nil, err
	}
	return &pb.AgentMessage{Msg: &pb.AgentMessage_Hello{Hello: &pb.Hello{
		AgentId: a.id.AgentID, SchemaVersion: uint32(pb.SchemaVersion_SCHEMA_VERSION_CURRENT), AgentVersion: "loadtest",
		Host: hostInfo(a.cfg, a.idx), CertPem: []byte(a.id.CertPEM), UnixMs: ms, Signature: sig, WritePolicy: "server",
	}}}, nil
}

// session runs one connection until it fails or ctx ends.
func (a *simAgent) session(ctx context.Context, first bool) error {
	conn, err := dial(a.cfg, &a.cert, "")
	if err != nil {
		a.st.connectFails.Add(1)
		return err
	}
	defer conn.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := pb.NewAgentServiceClient(conn).Connect(sctx)
	if err != nil {
		a.st.connectFails.Add(1)
		return err
	}
	h, err := a.hello()
	if err != nil {
		return err
	}
	t0 := time.Now()
	if err := a.send(stream, h); err != nil {
		a.st.connectFails.Add(1)
		return err
	}
	w, err := stream.Recv()
	if err != nil {
		a.st.connectFails.Add(1)
		return err
	}
	if gb := w.GetGoodbye(); gb != nil {
		a.st.connectFails.Add(1)
		return fmt.Errorf("goodbye: %s", gb.Reason)
	}
	if w.GetWelcome() == nil {
		a.st.connectFails.Add(1)
		return errors.New("expected welcome")
	}
	a.st.addHello(time.Since(t0))
	a.st.connected.Add(1)
	defer a.st.connected.Add(-1)

	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			if p := m.GetPing(); p != nil {
				_ = a.send(stream, &pb.AgentMessage{Msg: &pb.AgentMessage_Pong{Pong: &pb.Pong{UnixMs: time.Now().UnixMilli()}}})
			}
			// Jobs are ignored: the tool only measures reporting load.
		}
	}()

	// Like the real agent: report straight after connecting, inventory once
	// per connection, then on the interval.
	report := func() error {
		if err := a.send(stream, &pb.AgentMessage{Msg: &pb.AgentMessage_Metrics{Metrics: a.metrics()}}); err != nil {
			return err
		}
		a.st.reports.Add(1)
		if err := a.send(stream, &pb.AgentMessage{Msg: &pb.AgentMessage_State{State: a.state()}}); err != nil {
			return err
		}
		a.st.states.Add(1)
		return nil
	}
	if err := report(); err != nil {
		return err
	}
	if err := a.send(stream, &pb.AgentMessage{Msg: &pb.AgentMessage_Inventory{Inventory: a.inventory()}}); err != nil {
		return err
	}
	a.st.inventories.Add(1)

	tick := time.NewTicker(a.cfg.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-recvErr:
			return err
		case <-tick.C:
			if err := report(); err != nil {
				return err
			}
		}
	}
}
