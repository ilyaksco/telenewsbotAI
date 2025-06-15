package localization

import (
	"encoding/json"
	"io/fs"
	"log"
	"path/filepath"
)

type Localizer struct {
	messages map[string]map[string]string
}

func NewLocalizer(dir fs.FS) *Localizer {
	messages := make(map[string]map[string]string)

	files, err := fs.ReadDir(dir, "locales")
	if err != nil {
		log.Fatalf("Failed to read locales directory: %v", err)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			lang := file.Name()[:len(file.Name())-len(".json")]
			content, err := fs.ReadFile(dir, filepath.Join("locales", file.Name()))
			if err != nil {
				log.Printf("Failed to read locale file %s: %v", file.Name(), err)
				continue
			}

			var langMessages map[string]string
			if err := json.Unmarshal(content, &langMessages); err != nil {
				log.Printf("Failed to parse locale file %s: %v", file.Name(), err)
				continue
			}
			messages[lang] = langMessages
			log.Printf("Loaded language: %s", lang)
		}
	}

	return &Localizer{messages: messages}
}

func (l *Localizer) GetMessage(lang, key string) string {
	if langMessages, ok := l.messages[lang]; ok {
		if message, ok := langMessages[key]; ok {
			return message
		}
	}

	if defaultMessages, ok := l.messages["en"]; ok {
		if message, ok := defaultMessages[key]; ok {
			return message
		}
	}

	return key
}