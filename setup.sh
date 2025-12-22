#!/usr/bin/env sh

SESSION="auto"

tmux has-session -t "$SESSION" 2>/dev/null || {
  tmux new-session -d -s "$SESSION"
  tmux split-window -v -t "$SESSION":0
  tmux split-window -h -t "$SESSION":0.0
  tmux split-window -h -t "$SESSION":0.2

  tmux select-layout -t "$SESSION":0 tiled

  # 
  tmux respawn-pane -k -t "$SESSION":0.0 \
    "ngrok tcp 8081"

  tmux respawn-pane -k -t "$SESSION":0.1 \
    "ngrok tcp 8554"

  tmux respawn-pane -k -t "$SESSION":0.2 \
    "docker run --rm -p 8554:8554 bluenviron/mediamtx:latest"

  tmux respawn-pane -k -t "$SESSION":0.3 \
    "sleep 1 && ffmpeg \
      -re \
      -f lavfi -i testsrc=size=1920x1080:rate=30 \
      -c:v hevc_videotoolbox \
      -profile:v main \
      -b:v 4000k -maxrate 4000k -bufsize 8000k \
      -g 30 -keyint_min 30 -sc_threshold 0 \
      -pix_fmt yuv420p \
      -flags +global_header \
      -f rtsp -rtsp_transport tcp \
      rtsp://localhost:8554/test"
}

tmux attach -t "$SESSION"
