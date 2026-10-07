import { create } from "zustand";

// Reading a message aloud, with the browser's own voices (Web Speech). One
// message reads at a time: starting another stops the first.

interface SpeechState {
  /** reading is the key of the message being read, or "". */
  reading: string;
  speak: (key: string, markdown: string) => void;
  stop: () => void;
}

export const canSpeak = () => typeof window !== "undefined" && "speechSynthesis" in window;

/** spoken turns markdown into what is worth saying: no fences, marks or URLs. */
export function spoken(md: string): string {
  return md
    .replace(/```[\s\S]*?```/g, " (code block) ")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/https?:\/\/\S+/g, "link")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    .replace(/^\s*\|?[-:\s|]+\|?\s*$/gm, "")
    .replace(/\|/g, ", ")
    .replace(/[*_~>]+/g, "")
    .replace(/\n{2,}/g, ". ")
    .replace(/\s+/g, " ")
    .trim();
}

/** langOf guesses the language to read in: Vietnamese, Japanese, or English. */
export function langOf(text: string): string {
  if (/[ăâđêôơưạảấầẩẫậắằẳẵặẹẻẽếềểễệỉịọỏốồổỗộớờởỡợụủứừửữựỳỵỷỹ]/i.test(text)) return "vi-VN";
  if (/[぀-ヿ一-龯]/.test(text)) return "ja-JP";
  return "en-US";
}

export const useSpeech = create<SpeechState>((set, get) => ({
  reading: "",
  speak: (key, markdown) => {
    if (!canSpeak()) return;
    const synth = window.speechSynthesis;
    synth.cancel();
    const text = spoken(markdown);
    if (!text) return;
    const lang = langOf(text);
    const voice = synth.getVoices().find((v) => v.lang === lang) ?? synth.getVoices().find((v) => v.lang.startsWith(lang.slice(0, 2)));
    // Long text is read in sentences: some engines stop a single utterance
    // after a quarter of a minute or so.
    const parts = text.match(/[^.!?。！？]+[.!?。！？]*\s*/g) ?? [text];
    parts.forEach((p, i) => {
      const u = new SpeechSynthesisUtterance(p);
      u.lang = lang;
      if (voice) u.voice = voice;
      if (i === parts.length - 1) u.onend = u.onerror = () => get().reading === key && set({ reading: "" });
      synth.speak(u);
    });
    set({ reading: key });
  },
  stop: () => {
    if (canSpeak()) window.speechSynthesis.cancel();
    set({ reading: "" });
  },
}));
