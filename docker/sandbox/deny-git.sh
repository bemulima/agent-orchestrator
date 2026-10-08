#!/bin/sh
# The untrusted command image has no Git implementation or credential helpers.
# Readonly diffs/metadata are supplied by the trusted orchestrator/runner.
printf '%s\n' 'Git operations are reserved for the trusted orchestrator.' >&2
exit 126
