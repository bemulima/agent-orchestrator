package architecture

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const generatedHeader = "%% GENERATED FILE — DO NOT EDIT MANUALLY\n%% Architecture mode: CURRENT\n"

func SystemMermaid(current domain.ArchitectureCurrent) string {
	lines := []string{generatedHeader + "%% Topology revision: " + current.TopologyRevisionID, "flowchart LR"}
	for _, service := range current.Services {
		lines = append(lines, fmt.Sprintf("  %s[%s]", nodeID(service.ProjectID), label(service.Name)))
	}
	for _, relation := range current.Relations {
		lines = append(lines, edgeLine(relation))
	}
	return strings.Join(lines, "\n") + "\n"
}

func ServiceMermaid(detail domain.ArchitectureServiceDetail) string {
	lines := []string{generatedHeader + "%% Topology revision: " + detail.TopologyRevisionID, "flowchart LR", fmt.Sprintf("  %s[[%s]]", nodeID(detail.Service.ProjectID), label(detail.Service.Name))}
	seen := map[string]struct{}{detail.Service.ProjectID: {}}
	for _, relation := range append(append([]domain.ArchitectureRelation{}, detail.Inbound...), detail.Outbound...) {
		other := relation.SourceProjectID
		if other == detail.Service.ProjectID {
			other = relation.TargetProjectID
		}
		if _, ok := seen[other]; !ok {
			name := other
			for _, service := range detail.Services {
				if service.ProjectID == other {
					name = service.Name
				}
			}
			lines = append(lines, fmt.Sprintf("  %s[%s]", nodeID(other), label(name)))
			seen[other] = struct{}{}
		}
		lines = append(lines, edgeLine(relation))
	}
	return strings.Join(lines, "\n") + "\n"
}

func ContractsMermaid(detail domain.ArchitectureServiceDetail) string {
	lines := []string{generatedHeader + "%% Topology revision: " + detail.TopologyRevisionID, "flowchart LR", fmt.Sprintf("  %s[[%s]]", nodeID(detail.Service.ProjectID), label(detail.Service.Name))}
	contracts := append([]domain.ArchitectureContract(nil), detail.Contracts...)
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Code < contracts[j].Code })
	for _, contract := range contracts {
		if contract.ProjectID != detail.Service.ProjectID {
			continue
		}
		id := "contract_" + nodeID(contract.ID)
		text := string(contract.Type) + ": " + contract.Code
		if contract.Method != "" {
			text += " " + contract.Method
		}
		if contract.Path != "" {
			text += " " + contract.Path
		}
		if contract.Subject != "" {
			text += " " + contract.Subject
		}
		lines = append(lines, fmt.Sprintf("  %s[%s]", id, label(text)))
		lines = append(lines, fmt.Sprintf("  %s --> %s", nodeID(detail.Service.ProjectID), id))
	}
	return strings.Join(lines, "\n") + "\n"
}

func edgeLine(relation domain.ArchitectureRelation) string {
	text := string(relation.RelationType)
	if relation.Protocol != "" {
		text += " / " + string(relation.Protocol)
	}
	if relation.ContractCode != "" {
		text += " / " + relation.ContractCode
	}
	return fmt.Sprintf("  %s -->|%s| %s", nodeID(relation.SourceProjectID), label(text), nodeID(relation.TargetProjectID))
}
func nodeID(value string) string {
	var b strings.Builder
	b.WriteString("n_")
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
func label(value string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\"", "&#34;", "[", "(", "]", ")").Replace(value)
}
