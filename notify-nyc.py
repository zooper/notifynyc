import feedparser
import telegram
import time
import os

token = os.environ['TOKEN']
chat_id = os.environ['CHAT_ID']

# Telegram bot settings
bot = telegram.Bot(token=token)
chat_id = chat_id

url = 'https://a858-nycnotify.nyc.gov/RSS/NotifyNYC?lang=en'

# Remove everything in the message that contains below. 
sep = "To view this message"

def run_script():
    d = feedparser.parse(url)
    for post in d.entries:
        published = (post["published"])
        if not str(published) in open("/log/log.txt").read():
            message = (post["description"])
            log = open("/log/log.txt", "a")
            log.write(published + "\n")
            msg = post['title']
            msg += (message.split(sep, 1)[0])
            bot.send_message(
                    chat_id=chat_id,
                    text = msg,
                    parse_mode=telegram.ParseMode.HTML
                    )
            print(msg)

while True:
    run_script()
    time.sleep(300)
