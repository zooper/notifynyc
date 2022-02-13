FROM python:3.6


# Creating Application Source Code Directory
RUN mkdir -p /notifynyc
RUN mkdir /log

# Setting Home Directory for containers
WORKDIR /notifynyc

# Installing python dependencies
COPY requirements.txt /notifynyc
COPY run.sh /run.sh
RUN pip install --no-cache-dir -r requirements.txt
# RUN curl https://raw.githubusercontent.com/zooper/notifynyc/master/notify-nyc.py -o /notifynyc/notify-nyc.py
# Copying src code to Container
# COPY . /notifynyc/

# Running Python Application
CMD ["bash", "/run.sh"]
