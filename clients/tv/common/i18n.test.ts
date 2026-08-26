import { afterEach, describe, expect, it } from "vitest";
import { messagesForLocale, setLocale, supportedTvLocales, t } from "./i18n";

const englishEquivalentAllowlist: Record<string, true> = {
  "app.name": true,
  "server.example": true,
  "library.series": true,
  "media.runtime": true,
  "player.pause": true,
  "player.audio": true,
  "settings.server": true,
  "settings.serverValue": true,
  "settings.version": true,
  "network.mobile": true,
};

function placeholders(message: string): string[] {
  const values: string[] = [];
  const pattern = /\{[A-Za-z0-9]+\}/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(message)) !== null) values.push(match[0]);
  return values.sort();
}

describe("TV locale coverage", () => {
  afterEach(() => setLocale("en"));

  it("defines every English key explicitly in every supported locale without an implicit English fallback", () => {
    const english = messagesForLocale("en");
    const englishKeys = Object.keys(english).sort();

    for (const locale of supportedTvLocales) {
      const messages = messagesForLocale(locale);
      expect(Object.keys(messages).sort(), locale).toEqual(englishKeys);
      for (const key of englishKeys) {
        expect(messages[key as keyof typeof messages], `${locale}:${key}`).toBeTruthy();
        expect(placeholders(messages[key as keyof typeof messages]), `${locale}:${key}`).toEqual(placeholders(english[key as keyof typeof english]));
        if (locale !== "en" && !englishEquivalentAllowlist[key]) {
          expect(messages[key as keyof typeof messages], `${locale}:${key}`).not.toBe(english[key as keyof typeof english]);
        }
      }
      setLocale(locale);
      expect(t("translation.key.missing")).toBe("translation.key.missing");
    }
  });
});
