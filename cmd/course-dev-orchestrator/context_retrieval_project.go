package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	adapter "github.com/bemulima/agent-orchestrator/internal/adapters/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
)

func projectCommandInputs(values contextCommandFlags, request core.RetrievalRequest) (domain.RoutingResult, *domain.ContractPlan, []core.CoverageResult, agentcontrol.Catalog, error) {
	var route domain.RoutingResult
	if err := readContextCommandJSON(values.routePath, &route); err != nil {
		return route, nil, nil, agentcontrol.Catalog{}, err
	}
	var contract *domain.ContractPlan
	if values.contractPath != "" {
		contract = new(domain.ContractPlan)
		if err := readContextCommandJSON(values.contractPath, contract); err != nil {
			return route, nil, nil, agentcontrol.Catalog{}, err
		}
	}
	var coverage []core.CoverageResult
	if values.coveragePath != "" {
		var report planning.RoutingCoverageReport
		if err := readContextCommandJSON(values.coveragePath, &report); err != nil {
			return route, contract, nil, agentcontrol.Catalog{}, err
		}
		admitted := map[string]core.SourceAdmission{}
		for _, source := range request.Sources {
			id := source.RouteIdentity
			if id == "" {
				id = source.Identity
			}
			admitted[id] = source
		}
		var err error
		coverage, err = adapter.AdaptRoutingCoverage(report, route, contract, admitted)
		if err != nil {
			return route, contract, nil, agentcontrol.Catalog{}, err
		}
	}
	catalog, err := agentcontrol.LoadCatalog(os.DirFS("."))
	return route, contract, coverage, catalog, err
}

func writeProjectCommandReport(path string, report adapter.ProjectAnalysisScope) error {
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create new project analysis report: %w", err)
	}
	_, err = file.Write(append(body, '\n'))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func runProjectContextPrepare(values contextCommandFlags, request core.RetrievalRequest, output io.Writer) error {
	route, contract, coverage, catalog, err := projectCommandInputs(values, request)
	if err != nil {
		return err
	}
	pack, _, scope, err := adapter.PrepareProjectContext(context.Background(), request, route, contract, coverage, catalog)
	if err != nil {
		return fmt.Errorf("prepare project analysis: %w", err)
	}
	if err = writeProjectCommandReport(values.analysisReportPath, scope); err != nil {
		return err
	}
	return writeContextCommandPack(output, pack, values.format)
}
func runProjectContextExpand(values contextCommandFlags, request core.RetrievalRequest, base core.ContextPack, expand core.ExpandRequest, output io.Writer) error {
	route, contract, coverage, catalog, err := projectCommandInputs(values, request)
	if err != nil {
		return err
	}
	delta, _, scope, err := adapter.ExpandProjectContext(context.Background(), base, request, route, contract, coverage, catalog, expand)
	if err != nil {
		return fmt.Errorf("expand project analysis: %w", err)
	}
	if err = writeProjectCommandReport(values.analysisReportPath, scope); err != nil {
		return err
	}
	if values.format == "markdown" {
		return writeContextCommandDeltaMarkdown(output, delta)
	}
	return writeJSON(output, delta)
}
