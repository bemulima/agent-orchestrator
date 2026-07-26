package main

import (
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestReadCommandFileAcceptsBoundedStdin(t *testing.T) {
	text, err := readCommandFile("-", strings.NewReader("  Проверить пути workspace.\n"))

	require.NoError(t, err)
	require.Equal(t, "Проверить пути workspace.", text)
}

func TestReadCommandFileRejectsOversizedStdin(t *testing.T) {
	_, err := readCommandFile("-", strings.NewReader(strings.Repeat("x", (1<<20)+1)))

	require.ErrorIs(t, err, domain.ErrValidation)
}
