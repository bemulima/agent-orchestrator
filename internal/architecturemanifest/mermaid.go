package architecturemanifest

import (
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const mermaidHeader = "%% GENERATED — READ-ONLY PRESENTATION\n%% Do not edit; source of truth is the architecture manifest.\n"

// ServiceMermaid renders a service manifest and its operation manifests as a
// deterministic, read-only Mermaid presentation.
func ServiceMermaid(manifest domain.ArchitectureServiceManifest, operations []domain.ArchitectureOperationManifest) string {
	var b strings.Builder
	b.WriteString(mermaidHeader)
	b.WriteString("flowchart LR\n")
	b.WriteString("  svc[\"" + esc(serviceName(manifest)) + "\"]\n")
	b.WriteString("  svcInfo[\"purpose: " + esc(statement(manifest.Purpose)) + "<br/>confidence: " + confidence(manifest.Confidence) + "\"]\n")
	b.WriteString("  svc --> svcInfo\n")

	sectionStatements(&b, "responsibilities", manifest.Responsibilities, "svc")
	sectionStatements(&b, "capabilities", manifest.Capabilities, "svc")
	for i, resource := range manifest.OwnedResources {
		id := fmt.Sprintf("resource_%d", i)
		b.WriteString(fmt.Sprintf("  %s[\"owned resource: %s — %s\"]\n", id, esc(joinUnknown(resource.Type, resource.Name)), esc(statement(resource.Description))))
		b.WriteString("  svc --> " + id + "\n")
	}
	for i, in := range manifest.InboundInterfaces {
		id := fmt.Sprintf("inbound_%d", i)
		b.WriteString(fmt.Sprintf("  %s[\"inbound %s: %s — %s\"]\n", id, esc(unknown(in.Transport)), esc(unknown(in.Name)), esc(statement(in.Description))))
		b.WriteString("  " + id + " -->|inbound| svc\n")
	}
	for i, out := range manifest.OutboundDependencies {
		id := fmt.Sprintf("outbound_%d", i)
		b.WriteString(fmt.Sprintf("  %s[\"outbound %s: %s — %s\"]\n", id, esc(unknown(out.Transport)), esc(unknown(out.Target)), esc(statement(out.Description))))
		b.WriteString("  svc -->|" + esc(unknown(out.Direction)) + "| " + id + "\n")
	}
	for i, group := range manifest.EndpointGroups {
		id := fmt.Sprintf("endpoint_group_%d", i)
		b.WriteString(fmt.Sprintf("  %s[\"endpoint group %s: %s — %s\"]\n", id, esc(unknown(group.ID)), esc(unknown(group.Name)), esc(statement(group.Description))))
		b.WriteString("  svc --> " + id + "\n")
		for j, op := range group.Operations {
			b.WriteString(fmt.Sprintf("  %s_op_%d[\"operation: %s\"]\n  %s --> %s_op_%d\n", id, j, esc(unknown(op)), id, id, j))
		}
	}
	contractSection(&b, "produced", manifest.ProducedContracts, "svc", true)
	contractSection(&b, "consumed", manifest.ConsumedContracts, "svc", false)
	contractSection(&b, "published event", manifest.PublishedEvents, "svc", true)
	contractSection(&b, "subscribed event", manifest.SubscribedEvents, "svc", false)
	for i, op := range operations {
		id := fmt.Sprintf("operation_%d", i)
		b.WriteString(fmt.Sprintf("  %s[\"operation: %s (%s)\"]\n  svc --> %s\n", id, esc(unknown(op.ID)), esc(unknown(string(op.Type))), id))
	}
	return b.String()
}

// OperationMermaid renders one operation manifest as a deterministic,
// read-only Mermaid presentation.
func OperationMermaid(manifest domain.ArchitectureOperationManifest) string {
	var b strings.Builder
	b.WriteString(mermaidHeader)
	b.WriteString("flowchart TD\n")
	b.WriteString(fmt.Sprintf("  operation[\"%s — %s\"]\n", esc(unknown(manifest.ID)), esc(unknown(string(manifest.Type)))))
	addNode(&b, "identity", "identity: "+identity(manifest.Identity))
	addNode(&b, "access", "access: "+access(manifest.Access))
	addNode(&b, "trigger", "trigger: "+statement(manifest.Trigger.Description))
	addNode(&b, "input", "input: "+input(manifest.Input))
	addNode(&b, "business_task", "business task: "+statement(manifest.BusinessTask))
	steps := make([]string, 0, len(manifest.BusinessProcess))
	for _, step := range manifest.BusinessProcess {
		steps = append(steps, joinUnknown(step.ID, statement(step.Description)))
	}
	addNode(&b, "business_process", "business process: "+listOrUnknown(steps))
	addStatements(&b, "business_rules", "business rule", manifest.BusinessRules)
	addNode(&b, "implementation", "implementation: "+implementation(manifest.Implementation))
	data := make([]string, 0, len(manifest.DataAccess))
	for _, item := range manifest.DataAccess {
		data = append(data, joinUnknown(item.Resource, item.Access, statement(item.Description)))
	}
	addNode(&b, "data_access", "data access: "+listOrUnknown(data))
	ext := make([]string, 0, len(manifest.ExternalInteractions))
	for _, item := range manifest.ExternalInteractions {
		ext = append(ext, joinUnknown(item.Transport, item.Target, item.Contract, item.Direction, statement(item.Description)))
	}
	addNode(&b, "external_interactions", "external interactions: "+listOrUnknown(ext))
	effects := make([]string, 0, len(manifest.SideEffects))
	for _, item := range manifest.SideEffects {
		effects = append(effects, joinUnknown(item.Type, statement(item.Description)))
	}
	addNode(&b, "side_effects", "side effects: "+listOrUnknown(effects))
	addNode(&b, "output", "output: "+output(manifest.Output))
	errs := make([]string, 0, len(manifest.Errors))
	for _, item := range manifest.Errors {
		errs = append(errs, joinUnknown(item.Code, status(item.StatusCode), statement(item.Description)))
	}
	addNode(&b, "errors", "errors: "+listOrUnknown(errs))
	addNode(&b, "evidence", "evidence: "+evidenceList(manifest.Evidence)+"; confidence: "+confidence(manifest.Confidence))
	return b.String()
}

func addNode(b *strings.Builder, id, text string) {
	b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n  operation --> %s\n", id, esc(text), id))
}
func addStatements(b *strings.Builder, id, prefix string, values []domain.ArchitectureStatement) {
	vals := make([]string, 0, len(values))
	for _, v := range values {
		vals = append(vals, statement(v))
	}
	addNode(b, id, prefix+": "+listOrUnknown(vals))
}
func sectionStatements(b *strings.Builder, section string, values []domain.ArchitectureStatement, parent string) {
	id := "service_" + section
	vals := make([]string, 0, len(values))
	for _, v := range values {
		vals = append(vals, statement(v))
	}
	b.WriteString(fmt.Sprintf("  %s[\"%s: %s\"]\n  %s --> %s\n", id, section, esc(listOrUnknown(vals)), parent, id))
}

func contractSection(b *strings.Builder, kind string, values []domain.ArchitectureContractReference, parent string, outgoing bool) {
	for i, c := range values {
		id := fmt.Sprintf("contract_%s_%d", strings.ReplaceAll(kind, " ", "_"), i)
		text := joinUnknown(c.Transport, c.Code, c.Direction, statement(c.Description))
		b.WriteString(fmt.Sprintf("  %s[\"%s contract: %s\"]\n", id, kind, esc(text)))
		if outgoing {
			b.WriteString("  " + parent + " --> " + id + "\n")
		} else {
			b.WriteString("  " + id + " --> " + parent + "\n")
		}
	}
}

func identity(v domain.ArchitectureOperationIdentity) string {
	parts := []string{unknown(v.Transport)}
	if v.HTTP != nil {
		parts = append(parts, joinUnknown(v.HTTP.Method, v.HTTP.Path))
	}
	if v.NATS != nil {
		parts = append(parts, joinUnknown(v.NATS.Subject, v.NATS.Role, v.NATS.Queue, v.NATS.Mode))
	}
	if v.Worker != nil {
		parts = append(parts, unknown(v.Worker.Name))
	}
	if v.Scheduled != nil {
		parts = append(parts, joinUnknown(v.Scheduled.Name, v.Scheduled.Schedule))
	}
	return strings.Join(parts, " / ")
}
func access(v domain.ArchitectureOperationAccess) string {
	return joinUnknown(statement(v.Audience), statement(v.Authentication), statement(v.Authorization), statement(v.Idempotency))
}
func input(v domain.ArchitectureOperationInput) string {
	vals := []string{}
	for _, group := range []struct {
		name   string
		fields []domain.ArchitectureInputField
	}{{"path", v.PathParams}, {"query", v.Query}, {"headers", v.Headers}} {
		for _, f := range group.fields {
			vals = append(vals, group.name+"="+joinUnknown(f.Name, fmt.Sprintf("required=%t", f.Required), statement(f.Description)))
		}
	}
	if v.Body != nil {
		vals = append(vals, "body="+joinUnknown(v.Body.Name, statement(v.Body.Description)))
	}
	return listOrUnknown(vals)
}
func implementation(v domain.ArchitectureImplementationFlow) string {
	groups := []struct {
		name   string
		values []domain.ArchitectureEvidence
	}{{"router", v.Router}, {"handler", v.Handler}, {"use_cases", v.UseCases}, {"domain_services", v.DomainServices}, {"repositories", v.Repositories}}
	vals := []string{}
	for _, g := range groups {
		vals = append(vals, g.name+"="+evidenceList(g.values))
	}
	return strings.Join(vals, "; ")
}
func output(v domain.ArchitectureOperationOutput) string {
	vals := []string{}
	for _, r := range v.Responses {
		vals = append(vals, "response="+joinUnknown(status(r.StatusCode), statement(r.Description)))
	}
	if v.Result != nil {
		vals = append(vals, "result="+joinUnknown(v.Result.Name, statement(v.Result.Description)))
	}
	for _, e := range v.EmittedEvents {
		vals = append(vals, "event="+joinUnknown(e.Transport, e.Code, e.Direction, statement(e.Description)))
	}
	return listOrUnknown(vals)
}
func evidenceList(values []domain.ArchitectureEvidence) string {
	if len(values) == 0 {
		return "unknown"
	}
	vals := make([]string, 0, len(values))
	for _, e := range values {
		vals = append(vals, joinUnknown(e.SourcePath, e.Symbol, span(e.StartLine, e.EndLine), e.Checksum))
	}
	return strings.Join(vals, "; ")
}
func span(a, b int) string {
	if a == 0 && b == 0 {
		return ""
	}
	return fmt.Sprintf("lines %d-%d", a, b)
}
func statement(v domain.ArchitectureStatement) string { return unknown(v.Value) }
func confidence(v float64) string                     { return fmt.Sprintf("%.3g", v) }
func status(v int) string {
	if v == 0 {
		return "status unknown"
	}
	return fmt.Sprintf("status %d", v)
}
func listOrUnknown(values []string) string {
	if len(values) == 0 {
		return "unknown"
	}
	return strings.Join(values, "; ")
}
func joinUnknown(values ...string) string {
	out := []string{}
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, unknown(v))
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return strings.Join(out, " / ")
}
func unknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "unknown"
	}
	return v
}
func serviceName(v domain.ArchitectureServiceManifest) string {
	return joinUnknown(v.Identity.Name, v.ID)
}
func esc(v string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "&#34;", "\n", " ", "\r", " ", "[", "(", "]", ")", "<", "&lt;", "{", "(", "}", ")").Replace(v)
}
