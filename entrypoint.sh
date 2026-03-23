#!/bin/sh
# Start ydotoold in the background (required by ydotool).
ydotoold &
sleep 0.5
exec auto_typer "$@"
