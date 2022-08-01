#!/bin/bash

curl https://gitea.linuxburken.se/zooper/notifynyc/raw/branch/master/notify-nyc.py -o /notifynyc/notify-nyc.py
touch /log/log.txt
python /notifynyc/notify-nyc.py
