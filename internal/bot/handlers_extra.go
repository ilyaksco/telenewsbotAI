package bot

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const maxMessageLength = 4096

func (b *TelegramBot) handleAnalyzeLinksCommand(message *tgbotapi.Message) {
	url := message.CommandArguments()
	if url == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "<b>Usage:</b>\n<code>/analyzelinks &lt;URL&gt;</code>")
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	// Kirim pesan "sedang diproses"
	waitMsg, _ := b.api.Send(tgbotapi.NewMessage(message.Chat.ID, "Analyzing URL, please wait... 🔎"))

	// Panggil fungsi analisis dari fetcher
	analyzedLinks, err := b.fetcher.AnalyzePageLinks(url)
	if err != nil {
		log.Printf("Failed to analyze links for %s: %v", url, err)
		errorText := fmt.Sprintf("Failed to analyze URL. Error: %v", err)
		b.api.Send(tgbotapi.NewEditMessageText(message.Chat.ID, waitMsg.MessageID, errorText))
		return
	}

	// Hapus pesan "sedang diproses"
	b.api.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, waitMsg.MessageID))

	if len(analyzedLinks) == 0 {
		b.api.Send(tgbotapi.NewMessage(message.Chat.ID, "No links found on the page."))
		return
	}

	// Format hasil analisis menjadi pesan
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("<b>Analysis Result for:</b>\n<code>%s</code>\n\n", url))

	// Mengganti 'i' yang tidak terpakai dengan '_'
	for _, link := range analyzedLinks {
		// Batasi panjang teks agar tidak terlalu panjang
		linkText := strings.TrimSpace(link.Text)
		if len(linkText) > 80 {
			linkText = linkText[:77] + "..."
		}
		if linkText == "" {
			continue // Lewati link yang tidak memiliki teks
		}

		// Buat blok untuk setiap link
		block := fmt.Sprintf("<b>Link Text:</b> %s\n<pre>Href: %s\nClass: %s\nParent Class: %s</pre>\n\n", linkText, link.Href, link.Class, link.ParentClass)

		// Cek jika pesan akan melebihi batas, kirim pesan sebelumnya
		if builder.Len()+len(block) > maxMessageLength {
			msg := tgbotapi.NewMessage(message.Chat.ID, builder.String())
			msg.ParseMode = tgbotapi.ModeHTML
			msg.DisableWebPagePreview = true
			b.api.Send(msg)
			builder.Reset() // Reset builder untuk pesan berikutnya
			time.Sleep(1 * time.Second)
		}
		builder.WriteString(block)
	}

	// Kirim sisa pesan di builder
	if builder.Len() > 0 {
		msg := tgbotapi.NewMessage(message.Chat.ID, builder.String())
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true
		b.api.Send(msg)
	}
}