# Current Condition

A retro CRT-style weather terminal with a rotating 3D globe, live weather data, news ticker, internet radio, and mini arcade games.

![Current Condition](https://currentcondition.exe.xyz:8000/)

## Features

- **Live Weather Data** - Temperature, humidity, wind, air quality from your location
- **Rotating 3D Globe** - Mapbox-powered globe showing visitor locations as glowing dots
- **News Ticker** - Live news headlines, stock market data, and personalized greetings
- **Internet Radio** - Built-in lo-fi/vaporwave radio stations
- **Mini Arcade Games** - Snake, Tetris, Asteroids, and Pong with persistent high scores
- **Multiple Color Themes** - Green (classic), red, purple, grey, full color, and HDR modes
- **CRT Effects** - Scanlines, flicker, chromatic aberration, and screen curvature

## Tech Stack

- **Frontend**: Vanilla HTML/CSS/JavaScript with Mapbox GL JS
- **Backend**: Go with SQLite for high scores and visitor tracking
- **APIs**: ipapi.co (geolocation), Open-Meteo (weather), GNews (news)

## Running Locally

```bash
go run server.go
```

Then visit http://localhost:8000

## Controls

- **G** - Toggle game panel
- **Tab** - Cycle through games (includes "no game" to close)
- **Escape** - Close game panel
- **H** - View high scores (in game)
- **Arrow keys / WASD** - Game controls
- **Space** - Shoot (Asteroids) / Hard drop (Tetris)

## Live Demo

https://currentcondition.exe.xyz:8000/

---

## About

This app was **100% vibe coded** with the help of [Shelley](https://exe.dev), an AI coding agent on [exe.dev](https://exe.dev).

The author ([@consti](https://github.com/consti)) hasn't written a single line of code — and that is amazing. 🤯
