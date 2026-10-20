#!/bin/sh
exec "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/run-expire.sh" incr
