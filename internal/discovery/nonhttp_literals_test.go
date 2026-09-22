package discovery

import (
	"reflect"
	"sort"
	"testing"
)

func TestScannerNATSLocalPackageConstantsAndLiteralCollections(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{
		"go.mod": "module example.test/service\n",
		"domain/events.go": `package domain
type Subject string
const Created Subject = "course.created"
const Closed = "course." + "closed"
`,
		"transport/subjects.go": `package transport
import events "example.test/service/domain"
var subjects = []string{string(events.Created), events.Closed}
`,
		"transport/subscriber.go": `package transport
import (
 "github.com/nats-io/nats.go"
 events "example.test/service/domain"
)
func subscribe(conn *nats.Conn) {
 for _, subject := range subjects { conn.Subscribe(subject, consume) }
 conn.QueueSubscribe(events.Created, "workers", consume)
 for _, subject := range []string{"inline.one", "inline.two"} { conn.Subscribe(subject, consume) }
 const localSubject = "local.constant"
 localSubjects := []string{localSubject}
 for _, subject := range localSubjects { conn.Subscribe(subject, consume) }
}
func consume(msg *nats.Msg) {}
`,
	})
	var got []string
	for _, operation := range report.Operations {
		got = append(got, operation.Subject+" "+operation.Role)
	}
	sort.Strings(got)
	want := []string{"course.closed event_subscriber", "course.created event_subscriber", "inline.one event_subscriber", "inline.two event_subscriber", "local.constant event_subscriber"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
	for _, conflict := range report.Conflicts {
		if conflict.Name == "unresolved_operation" {
			t.Fatalf("literal source unexpectedly unresolved: %+v", conflict)
		}
	}
}

func TestScannerNATSCallbackMethodIsRequestHandler(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{
		"subscriber.go": `package fixture
import "github.com/nats-io/nats.go"
type Handlers struct{}
func(h *Handlers) Register(conn *nats.Conn) {
 conn.QueueSubscribe("images.find", "images", h.find)
 conn.Subscribe("images.changed", h.changed)
}
func(h *Handlers) changed(msg *nats.Msg) {}
`,
		"handler.go": `package fixture
import "github.com/nats-io/nats.go"
func(h *Handlers) find(msg *nats.Msg) { msg.Respond(nil) }
`,
	})
	var got []string
	for _, operation := range report.Operations {
		got = append(got, operation.Subject+" "+operation.Role)
	}
	sort.Strings(got)
	want := []string{"images.changed event_subscriber", "images.find request_handler"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
}

func TestScannerNATSUnresolvedCollectionsRemainUnknown(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{"subscriber.go": `package fixture
import "github.com/nats-io/nats.go"
const cycleA = cycleB
const cycleB = cycleA
var dynamic = []string{"known.partial", configuration.Subject}
func subscribe(conn *nats.Conn) {
 for _, subject := range dynamic { conn.Subscribe(subject, consume) }
 conn.Subscribe(cycleA, consume)
}
func consume(msg *nats.Msg) {}
`})
	if len(report.Operations) != 0 {
		t.Fatalf("unresolved collection invented identities: %+v", report.Operations)
	}
	if len(report.Conflicts) == 0 {
		t.Fatal("unresolved collection must retain conflict evidence")
	}
}
