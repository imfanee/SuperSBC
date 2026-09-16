#!/usr/bin/env bash
# keepalived notify hook: logs the transition. In-flight calls of the old
# master are lost (FreeSWITCH has no shared call state); their reservations
# are released by the reconciliation worker of the node that made them when
# it comes back, or after max_call_duration + orphan_timeout by any node.
logger -t supersbc-ha "keepalived transition: $1"
