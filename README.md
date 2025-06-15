# 🤖 News Bot for Telegram

![Go Version](https://img.shields.io/badge/Go-1.21%2B-blue.svg)
![License](https://img.shields.io/badge/License-MIT-green.svg)
![Built with Gemini](https://img.shields.io/badge/Built%20with-Gemini%20AI-blueviolet)

An advanced Telegram bot that automatically fetches news from various sources, summarizes it using AI (Google Gemini), and posts it to your Telegram channel or group.

---

## ✨ Key Features

-   🌐 **Multi-Source**: Fetches news from multiple sources simultaneously, supporting both RSS Feeds and direct web page scraping.
-   🗂️ **Flexible Configuration**: All news sources are managed in a single, easy-to-edit `sources.json` file.
-   📰 **Intelligent Scraper**: Capable of extracting the full clean content and main image from article pages for accurate results.
-   🧠 **AI Summaries**: Leverages Google Gemini to create concise and informative news summaries.
-   💾 **Persistent Anti-Duplicate**: Uses an SQLite database to ensure the same news is never posted twice.
-   🎛️ **Full Control**: The news checking schedule and post limit per cycle are easily configured in the `.env` file.
-   🧱 **Modular Codebase**: Built with a clean and organized Go project structure, making it easy to maintain and extend.

---

## 🚀 Getting Started

To get this bot running on your local machine, follow these steps.

### 📋 Prerequisites

-   [Go](https://go.dev/dl/) version 1.21 or newer installed.
-   A Telegram account.
-   An API Key for **Google Gemini**. Get one from [Google AI Studio](https://aistudio.google.com/).
-   A **Telegram Bot** Token. Get one from [@BotFather](https://t.me/BotFather).

### 🛠️ Installation

1.  **Clone this repository:**
    ```sh
    git clone [https://github.com/ilyaksco/news-bot-telegram.git](https://github.com/ilyaksco/news-bot-telegram.git)
    ```

2.  **Navigate into the project directory:**
    ```sh
    cd news-bot-telegram
    ```

3.  **Create your environment file from the example:**
    ```sh
    cp .env.example .env
    ```

4.  **Fill in the Configuration**: Open the `.env` file with a text editor and fill in all the variable values with your own keys and IDs.

5.  **Set Up News Sources**: Open the `sources.json` file and edit the list of news sources to your liking.

6.  **Install Go dependencies:**
    ```sh
    go mod tidy
    ```

7.  **Run the Bot!**
    ```sh
    go run main.go
    ```
    Your bot is now live and will start checking for news based on the schedule.

---

## ⚙️ Advanced Configuration

-   **News Sources (`sources.json`)**: Manage all your news sources here.
    -   `type`: Can be `"rss"` or `"scrape"`.
    -   `url`: The URL to the RSS feed or the site's homepage.
    -   `link_selector`: (Only for `scrape` type) The CSS selector to find article links on the page.
-   **Scheduling & AI Prompt (`.env`)**:
    -   Modify `SCHEDULE_INTERVAL_MINUTES` and `POST_LIMIT_PER_RUN` to control posting frequency.
    -   Modify `AI_PROMPT` to change the tone and style of the AI-generated summaries.

---

## 📄 License

This project is licensed under the MIT License.# news-bot-telegram
# news-bot-telegram
# news-bot-telegram
