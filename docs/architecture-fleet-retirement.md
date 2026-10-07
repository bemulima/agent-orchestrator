# Active and historical fleet exports

The read-only architecture exporter accepts the active40 owner lock after
retiring `bemulima/ms-infra-messaging` and `bemulima/ms-go-tarantool`. Those
canonical repositories and service aliases cannot appear in the active fleet
or its external owner cohort. Existing42 locks remain accepted for reproducible
historical exports. Other fleet sizes remain invalid. Identity, source revision,
raw Git blob checksums, sorting, declaration completeness and external owner
classification checks are unchanged.

Build the producer from a clean committed source revision with
`make architecture-export-build`, then invoke:

```sh
REPOSITORY_ALLOWED_ROOTS=/absolute/owner/object/stores \
  .cache/bin/architecture-export \
  --fleet-inputs /absolute/proof/architecture-fleet-inputs.v1.json \
  --fleet-roots /absolute/proof/fleet-roots.json
```

`fleet-roots.json` maps each exact source identity to its local Git object store.
This mode reads committed owner declarations and needs no database. An optional
paired `--inventory-root`/`--inventory-path` reads an inventory at the committed
producer revision. The existing persisted CURRENT mode remains read-only.
Active replacement lock/graph evidence must be exported from fresh published
owner declarations; historic graph bytes must never be filtered or relabeled as
current replacement evidence.
