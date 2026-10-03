# Pinned owner architecture diagnostic

This graph was produced deterministically by the exact published CDO producer `c1cfd967fc13bf73b747dd7dfb8ab3cff23979c8`, using the accompanying 42-repository immutable fleet input lock. Two exports were byte-identical; all 941 declaration pins were verified.

Status: **BLOCKED**, diagnostic only. Four unresolved target relations remain (Admin, Gateway, Teacher twice). This artifact does not establish `CDO_CURRENT_EXPORT_READY` or authorize a final Testing Policy bundle.

Graph artifact storage and producer commits are independent. The fleet locks the reviewed owner commits; later diagnostic storage commits do not rewrite that source snapshot.
