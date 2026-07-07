package keyboards

import "testing"

func TestAuth_HasRegisterAndLogin(t *testing.T) {
	kb := Auth()
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("Auth layout = %d rows, want 1 row of 2 buttons", len(kb.InlineKeyboard))
	}
	if got := *kb.InlineKeyboard[0][0].CallbackData; got != CallbackRegister {
		t.Errorf("button 0 data = %q, want %q", got, CallbackRegister)
	}
	if got := *kb.InlineKeyboard[0][1].CallbackData; got != CallbackLogin {
		t.Errorf("button 1 data = %q, want %q", got, CallbackLogin)
	}
}

func TestLogin_HasOnlyLogin(t *testing.T) {
	kb := Login()
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 1 {
		t.Fatalf("Login layout = %d rows, want 1 row of 1 button", len(kb.InlineKeyboard))
	}
	if got := *kb.InlineKeyboard[0][0].CallbackData; got != CallbackLogin {
		t.Errorf("button data = %q, want %q", got, CallbackLogin)
	}
}

func TestMainMenu_HasPlayProfileHelpLogout(t *testing.T) {
	kb := MainMenu()
	var got []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			got = append(got, *btn.CallbackData)
		}
	}
	want := []string{CallbackPlay, CallbackProfile, CallbackHelp, CallbackLogout}
	if len(got) != len(want) {
		t.Fatalf("MainMenu buttons = %v, want %v", got, want)
	}
	for i, data := range want {
		if got[i] != data {
			t.Errorf("button %d data = %q, want %q", i, got[i], data)
		}
	}
}

func TestCategory_HasSportMusicTech(t *testing.T) {
	kb := Category()
	var got []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			got = append(got, *btn.CallbackData)
		}
	}
	want := []string{CallbackCategoryPrefix + "SPORT", CallbackCategoryPrefix + "MUSIC", CallbackCategoryPrefix + "TECH"}
	if len(got) != len(want) {
		t.Fatalf("Category buttons = %v, want %v", got, want)
	}
	for i, data := range want {
		if got[i] != data {
			t.Errorf("button %d data = %q, want %q", i, got[i], data)
		}
	}
}

func TestAnswer_EncodesQuestionIDAndIndex(t *testing.T) {
	kb := Answer("q1", []string{"Paris", "London", "Berlin"})
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 3 {
		t.Fatalf("Answer layout = %d rows, want 1 row of 3 buttons", len(kb.InlineKeyboard))
	}
	want := []string{"answer:q1|0", "answer:q1|1", "answer:q1|2"}
	for i, w := range want {
		if got := *kb.InlineKeyboard[0][i].CallbackData; got != w {
			t.Errorf("button %d data = %q, want %q", i, got, w)
		}
	}
	if got := kb.InlineKeyboard[0][1].Text; got != "London" {
		t.Errorf("button 1 label = %q, want the choice text %q", got, "London")
	}
}

func TestWaiting_HasOnlyCancel(t *testing.T) {
	kb := Waiting()
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 1 {
		t.Fatalf("Waiting layout = %d rows, want 1 row of 1 button", len(kb.InlineKeyboard))
	}
	if got := *kb.InlineKeyboard[0][0].CallbackData; got != CallbackCancel {
		t.Errorf("button data = %q, want %q", got, CallbackCancel)
	}
}
