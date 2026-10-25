package xianyu

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const ChatContentVersion = 2

func DecodeChatContent(raw string) map[string]any { return decodeContent(raw) }

type ChatCardField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ChatCard contains display data only, never vendor templates or executable actions.
type ChatCard struct {
	Kind     string          `json:"kind"`
	Title    string          `json:"title"`
	Text     string          `json:"text,omitempty"`
	Image    string          `json:"image,omitempty"`
	Price    string          `json:"price,omitempty"`
	Tag      string          `json:"tag,omitempty"`
	URL      string          `json:"url,omitempty"`
	Fields   []ChatCardField `json:"fields,omitempty"`
	Fallback bool            `json:"fallback,omitempty"`
}

type ChatContent struct {
	Type   string
	Text   string
	Images []string
	Card   *ChatCard
}

// ParseChatContent is shared by history and live pushes. Decode the IM custom
// envelope before interpreting the actual Goofish contentType.
func ParseChatContent(content map[string]any) ChatContent {
	for depth := 0; depth < 3 && chatContentType(content) == 101; depth++ {
		decoded := decodeContent(text(object(content["custom"])["data"]))
		if decoded == nil {
			break
		}
		content = decoded
	}
	out := ChatContent{Type: "system", Images: []string{}}
	ct := chatContentType(content)
	if ct == 1 || (ct == 0 && content["text"] != nil) {
		out.Type, out.Text = "text", firstNonempty(text(object(content["text"])["text"]), scalarText(content["text"]))
		return out
	}
	if ct == 2 || (ct == 0 && (content["image"] != nil || content["picUrl"] != nil)) {
		out.Type = "image"
		if pics, ok := object(content["image"])["pics"].([]any); ok {
			for _, picture := range pics {
				if image := chatImageURL(text(object(picture)["url"])); image != "" {
					out.Images = append(out.Images, image)
				}
			}
		}
		if image := chatImageURL(text(content["picUrl"])); len(out.Images) == 0 && image != "" {
			out.Images = append(out.Images, image)
		}
		return out
	}
	if ct == 3 || content["audio"] != nil {
		out.Text = "[语音消息]"
		return out
	}
	if ct == 4 || content["video"] != nil {
		out.Text = "[视频消息]"
		return out
	}
	if ct == 27 || content["withdrawTip"] != nil {
		out.Text = firstNonempty(cardText(object(content["withdrawTip"])["withdrawText"]), "[消息已撤回]")
		return out
	}
	if summary := cardText(object(content["lite"])["summary"]); summary != "" {
		out.Text = summary
		return out
	}
	for _, field := range []string{"tip", "pageReminder", "rtc"} {
		if node := object(content[field]); node != nil {
			if value := firstNonempty(cardText(node["tip"]), cardText(node["text"]), cardText(node["content"]), cardText(node["title"])); value != "" {
				out.Text = value
				return out
			}
		}
	}
	for _, key := range []string{"itemCard", "textCard", "actionCard", "imageCard", "userCard", "dxCard", "locationCard", "dynamicContent"} {
		if root := object(content[key]); root != nil {
			out.Card = parseChatCard(key, root)
			out.Type, out.Text = "card", out.Card.Title
			return out
		}
	}
	if value := cardText(object(content["text"])["text"]); value != "" {
		out.Text = value
		return out
	}
	out.Type, out.Text = "card", "闲鱼消息"
	out.Card = &ChatCard{Kind: "unknown", Title: "闲鱼消息", Text: "此类内容请在闲鱼中查看完整详情。", Fallback: true}
	return out
}

func chatContentType(content map[string]any) int {
	if value, ok := content["contentType"].(int64); ok {
		return int(value)
	}
	return intSetting(content, "contentType")
}

func parseChatCard(key string, root map[string]any) *ChatCard {
	item := object(root["item"])
	main := object(item["main"])
	ex := object(main["exContent"])
	desc := object(ex["descInfo"])
	card := &ChatCard{Kind: "notice"}
	if key == "itemCard" {
		card.Kind = "item"
	}
	if key == "dxCard" || key == "dynamicContent" {
		card.Kind = "dynamic"
	}
	name := strings.ToLower(text(object(root["template"])["name"]))
	if strings.Contains(name, "trade") || strings.Contains(name, "order") || root["orderId"] != nil {
		card.Kind = "order"
	}
	card.Title = firstNonempty(cardText(root["title"]), cardText(main["title"]), cardText(ex["title"]), cardText(item["title"]))
	card.Text = firstNonempty(cardText(root["content"]), cardText(root["memo"]), cardText(root["text"]), cardText(ex["content"]), cardText(ex["desc"]), cardText(ex["notice"]))
	if title := cardText(desc["title"]); title != "" && title != card.Title {
		card.Text = firstNonempty(card.Text+"\n"+title, title)
	}
	card.Image = chatImageURL(firstNonempty(text(item["mainPic"]), text(root["mainPic"]), text(root["imageUrl"]), text(root["picUrl"]), text(desc["imgUrl"])))
	if card.Image == "" && (key == "imageCard" || key == "locationCard") {
		card.Image = chatImageURL(text(root["url"]))
	}
	card.Price = firstNonempty(cardText(item["price"]), cardText(root["price"]), cardText(ex["price"]), cardText(desc["price"]))
	card.Tag = firstNonempty(cardText(root["itemTip"]), cardText(root["itemTag"]), cardText(root["statusText"]), cardText(ex["tag"]))
	page := object(object(root["action"])["page"])
	card.URL = ChatCardURL(firstNonempty(text(page["url"]), text(main["targetUrl"]), text(root["targetUrl"]), text(item["url"])))
	if id := scalarText(item["itemId"]); card.URL == "" && key == "itemCard" && allDigits(id) {
		card.URL = "https://www.goofish.com/item?id=" + url.QueryEscape(id)
	}
	for _, field := range []struct{ key, label string }{{"orderId", "订单号"}, {"orderNo", "订单号"}, {"statusText", "状态"}} {
		if value := firstNonempty(cardText(root[field.key]), cardText(ex[field.key])); value != "" {
			card.Fields = append(card.Fields, ChatCardField{field.label, value})
		}
	}
	// Dynamic templates differ. Extract readable text without downloading or
	// evaluating their rendering code; the template URL is never exposed as an action.
	if card.Title == "" && card.Text == "" {
		values := []string{}
		collectCardText(root, 0, &values)
		if len(values) > 0 {
			card.Title = values[0]
			card.Text = strings.Join(values[1:], "\n")
		}
	}
	card.Fallback = card.Title == "" && card.Text == "" && card.Image == ""
	if card.Title == "" {
		if card.Kind == "item" {
			card.Title = "商品卡片"
		} else {
			card.Title = "闲鱼卡片"
		}
	}
	if card.Fallback {
		card.Text = "此类卡片请在闲鱼中查看完整详情。"
	}
	return card
}

func scalarText(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case int, int64, float64:
		return fmt.Sprint(value)
	}
	return ""
}

func cardText(value any) string {
	if node := object(value); node != nil {
		for _, key := range []string{"text", "value", "title", "priceText"} {
			if s := scalarText(node[key]); s != "" {
				value = s
				break
			}
		}
	}
	raw := scalarText(value)
	if !strings.Contains(raw, "<") {
		return raw
	}
	z := html.NewTokenizer(strings.NewReader(raw))
	var out strings.Builder
	skip := 0
	for {
		token := z.Next()
		if token == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			break
		}
		switch token {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			if string(name) == "script" || string(name) == "style" {
				skip++
			}
			if skip == 0 && (string(name) == "br" || string(name) == "p" || string(name) == "div") {
				out.WriteString("\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if (string(name) == "script" || string(name) == "style") && skip > 0 {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				out.Write(z.Text())
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func collectCardText(value any, depth int, values *[]string) {
	if depth > 7 || len(*values) >= 12 {
		return
	}
	switch value := value.(type) {
	case map[string]any:
		for _, key := range []string{"title", "text", "content", "memo", "desc", "description", "notice", "subtitle"} {
			if s := cardText(value[key]); s != "" && !strings.HasPrefix(s, "http") {
				found := false
				for _, existing := range *values {
					if existing == s {
						found = true
						break
					}
				}
				if !found {
					*values = append(*values, s)
				}
			}
		}
		for _, key := range []string{"item", "main", "exContent", "descInfo", "data", "children", "nodes", "list"} {
			collectCardText(value[key], depth+1, values)
		}
	case []any:
		for _, item := range value {
			collectCardText(item, depth+1, values)
		}
	}
}

func chatImageURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

// Only expose ordinary platform navigation. Native item links have a known web
// equivalent; other native actions remain readable cards without an action button.
func ChatCardURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if u.User != nil {
		return ""
	}
	if u.Scheme == "fleamarket" && strings.Contains(strings.ToLower(u.Host+u.Path), "item") {
		id := firstNonempty(u.Query().Get("itemId"), u.Query().Get("id"))
		if allDigits(id) {
			return "https://www.goofish.com/item?id=" + url.QueryEscape(id)
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"goofish.com", "taobao.com", "tb.cn", "xianyu.mobi"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return u.String()
		}
	}
	return ""
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
