#!/bin/bash

curl http://gitea.srv.linuxburken.se:3000/zooper/notifynyc/raw/branch/master/notify-nyc.py -o /notifynyc/notify-nyc.py
touch /log/log.txt
python /notifynyc/notify-nyc.py
