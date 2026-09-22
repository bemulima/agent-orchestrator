package discovery

import (
	"reflect"
	"sort"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestScannerNonHTTPNATSInboundOperations(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{"subscriber.go": `package fixture
import "github.com/nats-io/nats.go"
const prefix = "course."
func subscribe(nc *nats.Conn, subject string) {
 nc.Subscribe(prefix + "created", func(msg *nats.Msg) { consume(msg.Data) })
 nc.QueueSubscribe("auth.verify", "auth", func(msg *nats.Msg) { msg.Respond(nil) })
 nc.Subscribe("user.lookup", lookup)
 nc.Request("outbound.request", nil, 0)
 nc.Publish("outbound.event", nil)
 nc.Subscribe(subject, func(msg *nats.Msg) {})
}
func lookup(msg *nats.Msg) { msg.RespondMsg(&nats.Msg{}) }
`})
	var got []string
	for _, operation := range report.Operations {
		got = append(got, operation.Subject+" "+string(operation.Type)+" "+operation.Role)
		if operation.SourcePath != "subscriber.go" || operation.SourceLine <= 0 || operation.Confidence <= 0 {
			t.Fatalf("missing source evidence: %+v", operation)
		}
	}
	sort.Strings(got)
	want := []string{
		"auth.verify " + string(domain.ArchitectureOperationNATSRequestReply) + " request_handler",
		"course.created " + string(domain.ArchitectureOperationNATSEventSubscriber) + " event_subscriber",
		"user.lookup " + string(domain.ArchitectureOperationNATSRequestReply) + " request_handler",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inbound operations = %v, want %v", got, want)
	}
	unresolved := false
	for _, conflict := range report.Conflicts {
		if conflict.Name == "unresolved_operation" {
			unresolved = true
		}
	}
	if !unresolved {
		t.Fatal("dynamic subject must be reported as unresolved")
	}
}

func TestScannerNonHTTPRequiresConsumedTickerAndLaunchedWorker(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{"jobs.go": `package fixture
import clock "time"
func scheduled() {
 ticker := clock.NewTicker(5 * clock.Second)
 for { select { case <-ticker.C: flush(); case <-done: return } }
}
func tickRange() { for range clock.Tick(clock.Minute) { flush() } }
func tickVariable() { ticks := clock.Tick(clock.Second); for range ticks { flush() } }
func unusedTicker() { ticker := clock.NewTicker(clock.Second); for _, item := range items { save(item) }; _ = ticker }
func singleReceive() { ticker := clock.NewTicker(clock.Second); <-ticker.C }
func main() { go scheduled(); go tickRange(); go tickVariable(); go eventWorker(); go oneShotWorker() }
func eventWorker() { for item := range items { save(item) } }
func oneShotWorker() { save(nil) }
func dormantWorker() { for { save(nil) } }
`})
	var got []string
	for _, operation := range report.Operations {
		got = append(got, operation.Name+" "+operation.Protocol)
		if operation.Protocol == "scheduled" && operation.Schedule == "" {
			t.Fatal("scheduled operation lacks source schedule expression")
		}
	}
	sort.Strings(got)
	want := []string{"./eventWorker worker", "./scheduled scheduled", "./tickRange scheduled", "./tickVariable scheduled"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("background operations = %v, want %v", got, want)
	}
}

func TestScannerScheduledRequiresBackgroundLifecycle(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{
		"cmd/service/main.go": `package main
import "time"
func main() {
 go cleanup()
 go func() { ticker := time.NewTicker(time.Minute); for range ticker.C { flush() } }()
 register(func() { ticker := time.NewTicker(time.Second); for range ticker.C { heartbeat() } })
}
func cleanup() { ticker := time.NewTicker(time.Minute); for range ticker.C { flush() } }
func dormant() { ticker := time.NewTicker(time.Second); for range ticker.C { flush() } }
`,
		"internal/adapters/http/handlers/ui.go": `package handlers
import ("time"; "net/http")
func Events(w http.ResponseWriter, r *http.Request) {
 go heartbeat()
 ticker := time.NewTicker(time.Second); for range ticker.C { poll() }
}
func heartbeat() { ticker := time.NewTicker(time.Second); for range ticker.C { flush() } }
`,
		"internal/activities/plan.go": `package activities
import "time"
func Run() { go func() { ticker := time.NewTicker(time.Second); for range ticker.C { heartbeat() } }() }
`,
		"internal/sink/buffer.go": `package sink
import "time"
type Sink struct{}
func NewSink() *Sink { s := &Sink{}; go s.run(); return s }
func (s *Sink) run() { ticker := time.NewTicker(time.Second); for range ticker.C { flush() } }
`,
		"internal/usecase/worker.go": `package usecase
import "time"
type Worker struct{}
func (w Worker) Run() { ticker := time.NewTicker(time.Second); for range ticker.C { process() } }
`,
	})
	var got []string
	for _, operation := range report.Operations {
		if operation.Type == domain.ArchitectureOperationScheduled {
			got = append(got, operation.Name)
		}
	}
	sort.Strings(got)
	want := []string{"cmd/service/cleanup", "cmd/service/main", "internal/sink/Sink.run", "internal/usecase/Worker.Run"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduled operations = %v, want %v", got, want)
	}
}

func TestScannerNonHTTPIgnoresNonProductionAndUnrelatedAPIs(t *testing.T) {
	source := `package fixture
import "github.com/nats-io/nats.go"
func subscribe(nc *nats.Conn) { nc.Subscribe("test.only", func(msg *nats.Msg) {}) }
`
	report := scanGoRouteFixture(t, map[string]string{
		"subscriber_test.go":  source,
		"examples/example.go": source,
		"broken.go":           "package broken {",
		"other.go": `package other
func subscribe(bus Bus) { bus.Subscribe("unrelated.subscription", callback) }
`,
	})
	if len(report.Operations) != 0 {
		t.Fatalf("nonproduction/unrelated APIs emitted operations: %+v", report.Operations)
	}
}
