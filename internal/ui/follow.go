package ui

// Following the answer down, until you scroll away.
//
// A streaming answer moves the transcript to its bottom on every delta, which
// is right while you are reading the newest line and wrong the moment you are
// not: scrolling up to reread something mid-turn was undone by the next token,
// a few times a second, for as long as the turn lasted. The reader's hand on
// the wheel is the stronger signal, so it wins.
//
// So the transcript follows the bottom only while you are at it. Scroll up and
// it stays where you put it, and its title says there is more below. Scroll
// back down to the bottom — or press End, or send a prompt, or switch session
// — and it follows again. That is how every chat and log viewer people already
// use behaves, which is the reason to behave that way here.

// followChat keeps the transcript at its bottom, unless the reader has
// scrolled away from it. Every automatic move goes through this.
//
// It pins rather than only moves. The new text reaches the viewport while
// drawing, a frame after the event that brought it, so going to the bottom
// now goes to the bottom of the shorter text and lands a delta short — which
// is why a finished answer could stop a few lines above its end. The pin is
// honoured by setChatContent, after the text has arrived.
func (m *Model) followChat() {
	if !m.chatAway {
		m.chat.GotoBottom()
		m.chatPin = true
	}
}

// toBottom goes to the bottom and follows from there. It is for what the
// reader does on purpose: sending a prompt, pressing End.
func (m *Model) toBottom() {
	m.chatAway = false
	m.chat.GotoBottom()
	m.chatPin = true
}

// noteChatScroll records where a scroll the reader made left the transcript:
// away from the bottom stops the following, back at it starts it again.
func (m *Model) noteChatScroll() {
	m.chatAway = !m.chat.AtBottom()
	if m.chatAway {
		m.chatPin = false
	}
}

// chatBelow says the transcript has newer lines below the view that it is not
// following, for the pane's title.
func (m *Model) chatBelow() bool {
	return m.chatAway && !m.chat.AtBottom()
}
