FROM python:3.6
MAINTAINER zooper

# Creating Application Source Code Directory
RUN mkdir -p /notifynyc/logs

# Setting Home Directory for containers
WORKDIR /notifynyc

# Installing python dependencies
COPY requirements.txt /notifynyc
RUN pip install --no-cache-dir -r requirements.txt

# Copying src code to Container
COPY . /notifynyc/

# Running Python Application
CMD ["python", "notify-nyc.py"]
