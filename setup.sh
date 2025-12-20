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
    "sleep 1 && ffmpeg -re -stream_loop -1 -f lavfi \
     -i testsrc=size=1920x1080:rate=30 \
     -c:v libx264 \
     -profile:v baseline -level 4.0 \
     -pix_fmt yuv420p \
     -preset veryfast -tune zerolatency \
     -g 60 -keyint_min 60 -bf 0 \
     -b:v 4M -maxrate 4M -bufsize 8M \
     -x264-params \"repeat-headers=1:open_gop=0\" \
     -f rtsp -rtsp_transport tcp rtsp://localhost:8554/test"
}

tmux attach -t "$SESSION"

# tmux has-session -t "$SESSION" 2>/dev/null || {
#   tmux new-session -d -s "$SESSION" \
#     "ngrok tcp 8081"

#   tmux split-window -v -t "$SESSION":0 \
#     "ngrok tcp 8554"

#   tmux split-window -h -t "$SESSION":0.0 \
#     "docker run --rm -p 8554:8554 bluenviron/mediamtx:latest"
  
#   tmux split-window -h -t "$SESSION":0.1 \
    # "sleep 1 && ffmpeg -re -stream_loop -1 -f lavfi \
    #  -i testsrc=size=1920x1080:rate=30 \
    #  -c:v libx264 \
    #  -profile:v baseline -level 4.0 \
    #  -pix_fmt yuv420p \
    #  -preset veryfast -tune zerolatency \
    #  -g 60 -keyint_min 60 -bf 0 \
    #  -b:v 4M -maxrate 4M -bufsize 8M \
    #  -x264-params \"repeat-headers=1:open_gop=0\" \
    #  -f rtsp -rtsp_transport tcp rtsp://localhost:8554/test"
# }
