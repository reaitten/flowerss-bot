package bot

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/reaitten/flowerss-bot/internal/config"
	"github.com/reaitten/flowerss-bot/internal/model"

	"go.uber.org/zap"
	tb "gopkg.in/tucnak/telebot.v2"
)

// FeedForChannelRegister register fetcher for channel
func FeedForChannelRegister(m *tb.Message, url string, channelMention string) {
	msg, err := B.Send(m.Chat, "Processing...")
	channelChat, err := B.ChatByID(channelMention)
	adminList, err := B.AdminsOf(channelChat)

	senderIsAdmin := false
	botIsAdmin := false
	for _, admin := range adminList {
		if m.Sender.ID == admin.User.ID {
			senderIsAdmin = true
		}
		if B.Me.ID == admin.User.ID {
			botIsAdmin = true
		}
	}

	if !botIsAdmin {
		msg, _ = B.Edit(msg, fmt.Sprintf("Please add bot as a channel manager first"))
		return
	}

	if !senderIsAdmin {
		msg, _ = B.Edit(msg, fmt.Sprintf("Non-channel managers cannot perform this operation"))
		return
	}

	source, err := model.FindOrNewSourceByUrl(url)

	if err != nil {
		msg, _ = B.Edit(msg, fmt.Sprintf("%s, Subscription failed", err))
		return
	}

	err = model.RegistFeed(channelChat.ID, source.ID)
	zap.S().Infof("%d for %d subscribe [%d]%s %s", m.Chat.ID, channelChat.ID, source.ID, source.Title, source.Link)

	if err == nil {
		newText := fmt.Sprintf(
			"Channel [%s](https://t.me/%s) subscription [%s](%s) success",
			channelChat.Title,
			channelChat.Username,
			source.Title,
			source.Link,
		)
		_, err = B.Edit(msg, newText,
			&tb.SendOptions{
				DisableWebPagePreview: true,
				ParseMode:             tb.ModeMarkdown,
			})
	} else {
		_, _ = B.Edit(msg, "Subscription failed")
	}
}

func registFeed(chat *tb.Chat, url string) {
	msg, err := B.Send(chat, "Processing...")
	url = model.ProcessWechatURL(url)
	source, err := model.FindOrNewSourceByUrl(url)

	if err != nil {
		msg, _ = B.Edit(msg, fmt.Sprintf("%s, Subscription failed", err))
		return
	}

	err = model.RegistFeed(chat.ID, source.ID)
	zap.S().Infof("%d subscribe [%d]%s %s", chat.ID, source.ID, source.Title, source.Link)

	if err == nil {
		_, _ = B.Edit(msg, fmt.Sprintf("[%s](%s) Successfully subscribed", source.Title, source.Link),
			&tb.SendOptions{
				DisableWebPagePreview: true,
				ParseMode:             tb.ModeMarkdown,
			})
	} else {
		_, _ = B.Edit(msg, "Subscription failed")
	}
}

//SendError send error user
func SendError(c *tb.Chat) {
	_, _ = B.Send(c, "Please enter the correct instruction!")
}

//BroadcastNews send new contents message to subscriber
func BroadcastNews(source *model.Source, subs []*model.Subscribe, contents []*model.Content) {
	zap.S().Infow("broadcast news",
		"fetcher id", source.ID,
		"fetcher title", source.Title,
		"subscriber count", len(subs),
		"new contents", len(contents),
	)

	for _, content := range contents {
		previewText := trimDescription(content.Description, config.PreviewText)

		for _, sub := range subs {
			tpldata := &config.TplData{
				SourceTitle:     source.Title,
				ContentTitle:    content.Title,
				RawLink:         content.RawLink,
				PreviewText:     previewText,
				TelegraphURL:    content.TelegraphURL,
				Tags:            sub.Tag,
				EnableTelegraph: sub.EnableTelegraph == 1 && content.TelegraphURL != "",
			}

			u := &tb.User{
				ID: int(sub.UserID),
			}
			o := &tb.SendOptions{
				DisableWebPagePreview: config.DisableWebPagePreview,
				ParseMode:             config.MessageMode,
				DisableNotification:   sub.EnableNotification != 1,
			}
			msg, err := tpldata.Render(config.MessageMode)
			if err != nil {
				zap.S().Errorw("broadcast news error, tpldata.Render err",
					"error", err.Error(),
				)
				return
			}
			if _, err := B.Send(u, msg, o); err != nil {

				if strings.Contains(err.Error(), "Forbidden") {
					zap.S().Errorw("broadcast news error, bot stopped by user",
						"error", err.Error(),
						"user id", sub.UserID,
						"source id", sub.SourceID,
						"title", source.Title,
						"link", source.Link,
					)
					sub.Unsub()
				}

				/*
					Telegram return error if markdown message has incomplete format.
					Print the msg to warn the user
					api error: Bad Request: can't parse entities: Can't find end of the entity starting at byte offset 894
				*/
				if strings.Contains(err.Error(), "parse entities") {
					zap.S().Errorw("broadcast news error, markdown error",
						"markdown msg", msg,
						"error", err.Error(),
					)
				}
			}
		}
	}
}

// BroadcastSourceError send fetcher updata error message to subscribers
func BroadcastSourceError(source *model.Source) {
	subs := model.GetSubscriberBySource(source)
	var u tb.User
	for _, sub := range subs {
		message := fmt.Sprintf("[%s](%s) Accumulatively %d consecutive update failed, temporarily stop the update", source.Title, source.Link, config.ErrorThreshold)
		u.ID = int(sub.UserID)
		_, _ = B.Send(&u, message, &tb.SendOptions{
			ParseMode: tb.ModeMarkdown,
		})
	}
}

// CheckAdmin check user is admin of group/channel
func CheckAdmin(upd *tb.Update) bool {

	if upd.Message != nil {
		if HasAdminType(upd.Message.Chat.Type) {
			adminList, _ := B.AdminsOf(upd.Message.Chat)
			for _, admin := range adminList {
				if admin.User.ID == upd.Message.Sender.ID {
					return true
				}
			}

			return false
		}

		return true
	} else if upd.Callback != nil {
		if HasAdminType(upd.Callback.Message.Chat.Type) {
			adminList, _ := B.AdminsOf(upd.Callback.Message.Chat)
			for _, admin := range adminList {
				if admin.User.ID == upd.Callback.Sender.ID {
					return true
				}
			}

			return false
		}

		return true
	}
	return false
}

// IsUserAllowed check user is allowed to use bot
func isUserAllowed(upd *tb.Update) bool {
	if upd == nil {
		return false
	}

	var userID int64

	if upd.Message != nil {
		userID = int64(upd.Message.Sender.ID)
	} else if upd.Callback != nil {
		userID = int64(upd.Callback.Sender.ID)
	} else {
		return false
	}

	if len(config.AllowUsers) == 0 {
		return true
	}

	for _, allowUserID := range config.AllowUsers {
		if allowUserID == userID {
			return true
		}
	}

	zap.S().Infow("user not allowed", "userID", userID)
	return false
}

func userIsAdminOfGroup(userID int, groupChat *tb.Chat) (isAdmin bool) {

	adminList, err := B.AdminsOf(groupChat)
	isAdmin = false

	if err != nil {
		return
	}

	for _, admin := range adminList {
		if userID == admin.User.ID {
			isAdmin = true
		}
	}
	return
}

// UserIsAdminChannel check if the user is the administrator of channel
func UserIsAdminChannel(userID int, channelChat *tb.Chat) (isAdmin bool) {
	adminList, err := B.AdminsOf(channelChat)
	isAdmin = false

	if err != nil {
		return
	}

	for _, admin := range adminList {
		if userID == admin.User.ID {
			isAdmin = true
		}
	}
	return
}

// HasAdminType check if the message is sent in the group/channel environment
func HasAdminType(t tb.ChatType) bool {
	hasAdmin := []tb.ChatType{tb.ChatGroup, tb.ChatSuperGroup, tb.ChatChannel, tb.ChatChannelPrivate}
	for _, n := range hasAdmin {
		if t == n {
			return true
		}
	}
	return false
}

// GetMentionFromMessage get message mention
func GetMentionFromMessage(m *tb.Message) (mention string) {
	if m.Text != "" {
		for _, entity := range m.Entities {
			if entity.Type == tb.EntityMention {
				if mention == "" {
					mention = m.Text[entity.Offset : entity.Offset+entity.Length]
					return
				}
			}
		}
	} else {
		for _, entity := range m.CaptionEntities {
			if entity.Type == tb.EntityMention {
				if mention == "" {
					mention = m.Caption[entity.Offset : entity.Offset+entity.Length]
					return
				}
			}
		}
	}
	return
}

var relaxUrlMatcher = regexp.MustCompile(`^(https?://.*?)($| )`)

// GetURLAndMentionFromMessage get URL and mention from message
func GetURLAndMentionFromMessage(m *tb.Message) (url string, mention string) {
	for _, entity := range m.Entities {
		if entity.Type == tb.EntityMention {
			if mention == "" {
				mention = m.Text[entity.Offset : entity.Offset+entity.Length]

			}
		}

		if entity.Type == tb.EntityURL {
			if url == "" {
				url = m.Text[entity.Offset : entity.Offset+entity.Length]
			}
		}
	}

	var payloadMatching = relaxUrlMatcher.FindStringSubmatch(m.Payload)
	if url == "" && len(payloadMatching) > 0 && payloadMatching[0] != "" {
		url = payloadMatching[0]
	}

	return
}
