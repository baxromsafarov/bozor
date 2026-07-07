package transport

// botMessages — локализованные тексты сообщений бота.
type botMessages struct {
	askContact   string
	contactBtn   string
	confirmed    string
	notOwned     string
	invalidPhone string
	genericErr   string
}

// messagesFor возвращает набор сообщений для языка "uz" или "ru".
func messagesFor(lang string) botMessages {
	if lang == "uz" {
		return botMessages{
			askContact:   "Kirish uchun telefon raqamingizni yuboring 👇",
			contactBtn:   "Raqamni yuborish",
			confirmed:    "✅ Raqam tasdiqlandi.",
			notOwned:     "Iltimos, o'z raqamingizni pastdagi tugma orqali yuboring.",
			invalidPhone: "Telefon raqami noto'g'ri.",
			genericErr:   "Xatolik yuz berdi. Keyinroq urinib ko'ring.",
		}
	}
	return botMessages{
		askContact:   "Для входа отправьте номер телефона 👇",
		contactBtn:   "Отправить номер",
		confirmed:    "✅ Номер подтверждён.",
		notOwned:     "Пожалуйста, отправьте свой номер кнопкой ниже.",
		invalidPhone: "Некорректный номер телефона.",
		genericErr:   "Произошла ошибка. Попробуйте позже.",
	}
}
