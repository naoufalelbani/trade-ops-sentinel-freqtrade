package main

type inlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

type replyKeyboardMarkup struct {
	Keyboard        [][]keyboardButton `json:"keyboard"`
	ResizeKeyboard  bool               `json:"resize_keyboard,omitempty"`
	OneTimeKeyboard bool               `json:"one_time_keyboard,omitempty"`
}

type keyboardButton struct {
	Text string `json:"text"`
}

type tgUpdateResp struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgMessage struct {
	MessageID int     `json:"message_id"`
	Text      string  `json:"text"`
	From      *tgUser `json:"from,omitempty"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	Data    string     `json:"data"`
	From    *tgUser    `json:"from,omitempty"`
	Message *tgMessage `json:"message,omitempty"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}
