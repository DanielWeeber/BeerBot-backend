![image](logo.svg)



# 🍺 BeerBot

[![CI](https://github.com/DanielWeeber/beer-with-me/actions/workflows/ci.yml/badge.svg)](https://github.com/DanielWeeber/beer-with-me/actions/workflows/ci.yml)

**Give your colleagues a virtual beer to show your appreciation!**

---

## 🌟 Overview

Beer With Me is a Slack bot that lets you give virtual beers to your colleagues. It's a fun way to show appreciation and build team morale. The bot keeps track of who's given and received beers, and it even has a daily limit to keep things from getting out of hand.

## ✨ Features

*   **Give virtual beers:** Use a simple command to give a beer to a colleague.
*   **Track beers:** The bot keeps a record of all beers given and received.
*   **Daily limits:** Each user has a daily limit on how many beers they can give.
*   **REST API:** A simple REST API to query beer data.
*   **Prometheus metrics:** The bot exposes Prometheus metrics for monitoring.

## 🚀 Getting Started

### Prerequisites

*   [Docker](https://www.docker.com/)
*   [Docker Compose](https://docs.docker.com/compose/)
*   [Go](https://golang.org/)
*   A Slack workspace where you can install the bot

### Installation

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/DanielWeeber/beer-with-me.git
    cd beer-with-me/bot
    ```

2.  **Create a Slack app:**
    *   Go to [https://api.slack.com/apps](https://api.slack.com/apps) and create a new app.
    *   Enable "Socket Mode".
    *   Add the following bot token scopes: `chat:write`, `commands`, `im:history`, `im:read`, `im:write`, `mpim:history`, `mpim:read`, `mpim:write`, `users:read`, `users:read.email`.
    *   Install the app to your workspace.
    *   You will need the "Bot User OAuth Token" (starts with `xoxb-`) and the "App-Level Token" (starts with `xapp-`).

3.  **Configure the bot:**
    *   Create a `.env` file in the `bot` directory by copying the `.env.example` file.
    *   Set the following environment variables in the `.env` file:
        *   `BOT_TOKEN`: Your Slack bot token.
        *   `APP_TOKEN`: Your Slack app-level token.
        *   `CHANNEL_ID`: The ID of the channel where the bot should be active.
        *   `API_TOKEN`: A secret token for the REST API.

4.  **Run the bot:**
    ```bash
    docker-compose up -d
    ```

## 🛠️ Usage

Once the bot is running and has been added to a channel, you can start giving beers!

*   **Give a beer:**
    ```
    @colleague :beer:
    ```
    You can give multiple beers at once:
    ```
    @colleague :beer: :beer:
    ```
    You can also give beers to multiple colleagues at once:
    ```
    @colleague1 :beer: @colleague2 :beer:
    ```

*   **REST API:**
    *   `GET /api/given?user=<user_id>&date=<date>`: Get the number of beers a user has given on a specific date or date range.
    *   `GET /api/received?user=<user_id>&date=<date>`: Get the number of beers a user has received on a specific date or date range.

    The `date` parameter can be a single date in `YYYY-MM-DD` format or a relative date range, such as `-1y` for the last year, `-1m` for the last month, or `-7d` for the last 7 days.

    **Example using `curl`:**

    ```bash
    curl -H "Authorization: Bearer <your_api_token>" http://localhost:8080/api/given?user=<user_id>&date=<date>
    ```

## 🙌 Contributing

Contributions are welcome! Please feel free to submit a pull request.

## 📄 License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

---
