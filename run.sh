#!/bin/bash

curl https://raw.githubusercontent.com/zooper/notifynyc/master/notify-nyc.py -o /notifynyc/notify-nyc.py
touch /log/log.txt
python /notifynyc/notify-nyc.py
