# What is Breaklist?

Breaklist is a personal morning briefing that prints on a thermal receipt printer. Think of it as a tiny daily newspaper, just for your household.

Every morning it generates a slip of paper containing whatever you have switched on:

- 📝 **Tasks and reminders** — from a local file or your Dropbox list
- 📅 **Calendar** — Google Calendar or any Home Assistant calendar
- ☀️ **Weather** — today's strip, a multi-day outlook, your own backyard station, sunrise and moon
- 📰 **News** — NY Times headlines, Hacker News, "On this day" from Wikipedia
- 🏠 **Your house** — any Home Assistant sensor, switch or template
- 😂 **Fun** — jokes, quotes, trivia with the answer upside down, xkcd, a Far Side strip
- 🧒 **For a three-year-old** — a letter and number to trace, stars to count, a shape to colour, a maze, a silly joke, the weather in toddler words, a routine checklist, a doodle box and a Pokémon

The PDF is sized for 47 mm receipt paper (configurable). Print it, tear it off, and carry your day in your pocket.

## How it works

Run `breaklist open`, and a small web app appears in your browser. Tick the sections you want, drag them into order, connect the services you use, and set a schedule. Breaklist then generates and prints the report every morning on its own.

## The stack

| Part | Built with |
|------|-----------|
| Program and GUI server | Go, standard library, embedded HTML/JS |
| PDF conversion | [wkhtmltopdf](https://wkhtmltopdf.org/) |
| Weather | [Open-Meteo](https://open-meteo.com/) or [Tomorrow.io](https://www.tomorrow.io/) |
| Home automation | [Home Assistant REST API](https://developers.home-assistant.io/docs/api/rest/) |

## Who is it for?

Anyone who wants a low-tech, distraction-free way to start the day. No apps to open, no notifications, just paper. And a small person who gets their own page to scribble on.
