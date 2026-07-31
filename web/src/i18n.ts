const en = {
  "common.close": "Close",
  "common.back": "Back",
  "common.continue": "Continue",
  "common.cancel": "Cancel",
  "common.save": "Save changes",
  "common.delete": "Delete",
  "common.refresh": "Refresh",
  "common.loading": "Loading",
  "common.add": "Add",
  "common.edit": "Edit",
  "common.search": "Search",
  "common.play": "Play",
  "common.retry": "Try again",
  "common.noResults": "No results",
  "common.serverError": "The server could not complete this request.",
  "brand.tagline": "Your media universe, everywhere.",
  "auth.selfHosted": "Hosted by you",
  "auth.noCloud": "No mandatory cloud",
  "auth.welcomeBadge": "Your media library, reimagined",
  "auth.welcomeTitle": "Welcome to your new universe.",
  "auth.welcomeBody": "Rivune brings your sources, collections, and watch progress together in one private, personal experience.",
  "auth.configure": "Set up my space",
  "auth.setupTitle": "Let's get to know each other.",
  "auth.setupEyebrow": "First-time setup",
  "auth.setupBody": "This information stays exclusively on your server.",
  "auth.instanceName": "Space name",
  "auth.adminAccount": "Administrator account",
  "auth.firstProfile": "First profile",
  "auth.adminPassword": "Administrator password",
  "auth.passwordHint": "Use at least 12 characters and a unique password.",
  "auth.setupToken": "Setup token",
  "auth.setupTokenHint": "Contents of secrets/setup_token.txt",
  "auth.createSpace": "Create my space",
  "auth.readyTitle": "Your universe is ready.",
  "auth.readyBody": "Choose your profile to get started.",
  "auth.openSource": "Open source · Designed to stay yours",
  "auth.username": "Username",
  "auth.password": "Password",
  "auth.showPassword": "Show password",
  "auth.hidePassword": "Hide password",
  "auth.signIn": "Sign in",
  "auth.secureConnection": "Secure connection to your personal server",
  "auth.connectedTo": "Connected to {server}",
  "auth.setupFailure": "This server could not be initialized.",
  "auth.loginFailure": "Unable to sign in. Make sure the server is available.",
  "profiles.eyebrow": "Your personal space",
  "profiles.title": "Who's watching?",
  "profiles.body": "Choose a profile to enter your universe.",
  "profiles.child": "Kids profile",
  "profiles.admin": "Administrator",
  "profiles.standard": "Profile",
  "profiles.new": "New profile",
  "profiles.customize": "Customize",
  "profiles.signOut": "Disconnect device",
  "profiles.privacy": "Preferences and watch progress stay separate for every profile.",
  "profiles.protected": "Protected profile",
  "profiles.hello": "Hello, {name}",
  "profiles.pinBody": "Enter your PIN to continue.",
  "profiles.showPin": "Show PIN",
  "profiles.hidePin": "Hide PIN",
  "profiles.open": "Open profile",
  "profiles.openFailure": "This profile could not be opened.",
  "app.connecting": "Connecting to your universe…",
  "app.loadingSpace": "Loading your space…",
  "app.offlineTitle": "This server feels far away.",
  "app.offlineBody": "Rivune could not reach its backend. Check that the server is running, then try again.",
  "app.reconnect": "Reconnect",
  "auth.welcomeTitleLead": "Welcome to your",
  "auth.welcomeTitleAccent": "new universe.",
  "nav.home": "Home",
  "nav.search": "Search",
  "nav.library": "Library",
  "nav.calendar": "Calendar",
  "nav.manage": "Manage",
  "nav.administration": "Administration",
  "nav.preferences": "Preferences",
  "nav.main": "Main navigation",
  "nav.mobile": "Mobile navigation",
  "nav.browse": "Browse",
  "shell.manage": "Manage",
  "shell.preferences": "Preferences",
  "shell.connectedTo": "Connected to",
  "shell.switchProfile": "Switch profile",
  "shell.welcomeBack": "Welcome back, {name}",
  "shell.openMenu": "Open menu",
  "shell.closeMenu": "Close menu",
  "shell.collapseSidebar": "Compact sidebar",
  "shell.expandSidebar": "Expand sidebar",
  "media.open": "Open {title}",
} as const;

export type TranslationKey = keyof typeof en;
export type TranslationCatalog = Record<TranslationKey, string>;
type Replacements = Record<string, string | number>;

const catalogs = { en } satisfies Record<string, TranslationCatalog>;
export type Locale = keyof typeof catalogs;

function supportedLocale(candidate: string): candidate is Locale {
  return candidate in catalogs;
}

function resolveLocale(): Locale {
  const candidates = typeof navigator === "undefined" ? [] : navigator.languages;
  for (const candidate of candidates) {
    const normalized = candidate.toLowerCase();
    if (supportedLocale(normalized)) return normalized;
    const language = normalized.split("-")[0];
    if (supportedLocale(language)) return language;
  }
  return "en";
}

export const locale = resolveLocale();

export function translate(key: TranslationKey, replacements?: Replacements): string {
  let value: string = catalogs[locale][key];
  if (!replacements) return value;
  for (const [name, replacement] of Object.entries(replacements)) {
    value = value.replaceAll(`{${name}}`, String(replacement));
  }
  return value;
}
