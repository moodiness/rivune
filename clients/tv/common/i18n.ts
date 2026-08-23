export type TvLocale = "en" | "fr" | "es" | "de" | "it" | "pt-BR";

type Values = Record<string, string | number>;
type Messages = Record<string, string>;

const en: Messages = {
  "app.name": "Rivune",
  "app.tagline": "Your media universe, on every screen.",
  "common.back": "Back",
  "common.cancel": "Cancel",
  "common.close": "Close",
  "common.continue": "Continue",
  "common.loading": "Loading…",
  "common.retry": "Try again",
  "common.save": "Save",
  "common.select": "Select",
  "common.signOut": "Sign out",
  "server.title": "Connect to your Rivune server",
  "server.body": "Enter the HTTPS address of the server you self-host.",
  "server.address": "Server address",
  "server.example": "https://media.example.com",
  "server.connect": "Connect",
  "server.security": "HTTP is accepted only for localhost or a literal private-network address.",
  "server.incompatible": "This app implements protocol 20, but the server uses protocol {version}.",
  "pair.title": "Pair this TV",
  "pair.body": "Open {url} on another device and enter this code.",
  "pair.waiting": "Waiting for approval…",
  "pair.expires": "Code expires at {time}",
  "pair.restart": "Generate a new code",
  "profiles.title": "Who is watching?",
  "profiles.unavailable": "This profile is not currently available.",
  "pin.title": "Enter PIN for {name}",
  "pin.label": "Profile PIN",
  "pin.submit": "Unlock profile",
  "nav.home": "Home",
  "nav.search": "Search",
  "nav.library": "Library",
  "nav.calendar": "Calendar",
  "nav.settings": "Settings",
  "home.continue": "Continue watching",
  "home.empty": "Your configured collections will appear here.",
  "search.title": "Search",
  "search.placeholder": "Movies, series and live TV",
  "search.submit": "Search",
  "search.empty": "No results for this search.",
  "library.title": "Library",
  "library.all": "All",
  "library.movies": "Movies",
  "library.series": "Series",
  "library.live": "Live TV",
  "library.empty": "Your library is empty.",
  "calendar.title": "Calendar",
  "calendar.empty": "No upcoming releases.",
  "media.untitled": "Untitled",
  "media.play": "Play",
  "media.resume": "Resume at {time}",
  "media.sources": "Choose a source",
  "media.seasons": "Seasons",
  "media.episodes": "Episodes",
  "media.cast": "Cast",
  "media.runtime": "{minutes} min",
  "media.rating": "Rating {rating}",
  "source.title": "Choose a source",
  "source.empty": "No compatible source is available.",
  "source.partial": "Some providers could not respond.",
  "player.buffering": "Buffering…",
  "player.pause": "Pause",
  "player.resume": "Resume",
  "player.seekBack": "Back 10 seconds",
  "player.seekForward": "Forward 10 seconds",
  "player.audio": "Audio",
  "player.subtitles": "Subtitles",
  "player.off": "Off",
  "player.stop": "Stop",
  "settings.title": "Settings",
  "settings.server": "Server",
  "settings.serverValue": "{name} · {version}",
  "settings.profile": "Profile",
  "settings.changeProfile": "Change profile",
  "settings.changeServer": "Change server",
  "settings.platform": "Platform",
  "settings.version": "Rivune TV {version}",
  "settings.language": "Interface language",
  "settings.update.title": "Application updates",
  "settings.update.check": "Check for updates",
  "settings.update.retry": "Try again",
  "settings.update.download": "Download update",
  "settings.update.restart": "Restart and update",
  "settings.update.status.idle": "Automatic updates are enabled",
  "settings.update.status.checking": "Checking GitHub…",
  "settings.update.status.up-to-date": "Rivune TV is up to date",
  "settings.update.status.available": "An update is available",
  "settings.update.status.downloading": "Downloading and verifying…",
  "settings.update.status.ready": "Update verified and ready",
  "settings.update.status.unavailable": "Updates are unavailable on this installation",
  "settings.update.status.error": "The update check failed",
  "error.title": "Something went wrong",
  "error.network": "The server could not be reached.",
  "error.authorization": "Pairing was refused or expired.",
  "error.profile": "Select the profile again to continue.",
  "error.playback": "Playback could not start.",
};

const fr: Messages = {
  ...en,
  "app.tagline": "Votre univers multimédia, sur tous vos écrans.", "common.back": "Retour", "common.cancel": "Annuler", "common.close": "Fermer", "common.continue": "Continuer", "common.loading": "Chargement…", "common.retry": "Réessayer", "common.save": "Enregistrer", "common.select": "Sélectionner", "common.signOut": "Se déconnecter",
  "server.title": "Connectez-vous à votre serveur Rivune", "server.body": "Saisissez l’adresse HTTPS du serveur que vous auto-hébergez.", "server.address": "Adresse du serveur", "server.connect": "Se connecter", "server.security": "HTTP est accepté uniquement pour localhost ou une adresse privée littérale.", "server.incompatible": "Cette application utilise le protocole 20, mais le serveur utilise le protocole {version}.",
  "pair.title": "Associer ce téléviseur", "pair.body": "Ouvrez {url} sur un autre appareil et saisissez ce code.", "pair.waiting": "En attente de validation…", "pair.expires": "Le code expire à {time}", "pair.restart": "Générer un nouveau code",
  "profiles.title": "Qui regarde ?", "profiles.unavailable": "Ce profil n’est pas disponible actuellement.", "pin.title": "Saisissez le PIN de {name}", "pin.label": "PIN du profil", "pin.submit": "Déverrouiller le profil",
  "nav.home": "Accueil", "nav.search": "Recherche", "nav.library": "Bibliothèque", "nav.calendar": "Calendrier", "nav.settings": "Réglages", "home.continue": "Continuer à regarder", "home.empty": "Vos collections configurées apparaîtront ici.",
  "search.title": "Recherche", "search.placeholder": "Films, séries et TV en direct", "search.submit": "Rechercher", "search.empty": "Aucun résultat pour cette recherche.",
  "library.title": "Bibliothèque", "library.all": "Tout", "library.movies": "Films", "library.series": "Séries", "library.live": "TV en direct", "library.empty": "Votre bibliothèque est vide.", "calendar.title": "Calendrier", "calendar.empty": "Aucune sortie à venir.",
  "media.untitled": "Sans titre", "media.play": "Lire", "media.resume": "Reprendre à {time}", "media.sources": "Choisir une source", "media.seasons": "Saisons", "media.episodes": "Épisodes", "media.cast": "Distribution", "media.runtime": "{minutes} min", "media.rating": "Note {rating}",
  "source.title": "Choisir une source", "source.empty": "Aucune source compatible n’est disponible.", "source.partial": "Certains fournisseurs n’ont pas répondu.",
  "player.buffering": "Mise en mémoire tampon…", "player.pause": "Pause", "player.resume": "Reprendre", "player.seekBack": "Reculer de 10 secondes", "player.seekForward": "Avancer de 10 secondes", "player.audio": "Audio", "player.subtitles": "Sous-titres", "player.off": "Désactivés", "player.stop": "Arrêter",
  "settings.title": "Réglages", "settings.server": "Serveur", "settings.profile": "Profil", "settings.changeProfile": "Changer de profil", "settings.changeServer": "Changer de serveur", "settings.platform": "Plateforme", "settings.language": "Langue de l’interface", "settings.update.title": "Mises à jour de l’application", "settings.update.check": "Rechercher des mises à jour", "settings.update.retry": "Réessayer", "settings.update.download": "Télécharger la mise à jour", "settings.update.restart": "Redémarrer et mettre à jour", "settings.update.status.idle": "Les mises à jour automatiques sont activées", "settings.update.status.checking": "Recherche sur GitHub…", "settings.update.status.up-to-date": "Rivune TV est à jour", "settings.update.status.available": "Une mise à jour est disponible", "settings.update.status.downloading": "Téléchargement et vérification…", "settings.update.status.ready": "Mise à jour vérifiée et prête", "settings.update.status.unavailable": "Les mises à jour sont indisponibles sur cette installation", "settings.update.status.error": "La recherche de mise à jour a échoué", "error.title": "Une erreur est survenue", "error.network": "Le serveur est injoignable.", "error.authorization": "L’association a été refusée ou a expiré.", "error.profile": "Sélectionnez de nouveau le profil pour continuer.", "error.playback": "La lecture n’a pas pu démarrer."
};

const es: Messages = {
  ...en, "common.back": "Atrás", "common.cancel": "Cancelar", "common.close": "Cerrar", "common.loading": "Cargando…", "common.retry": "Reintentar", "server.title": "Conéctate a tu servidor Rivune", "server.body": "Introduce la dirección HTTPS del servidor que alojas.", "server.address": "Dirección del servidor", "server.connect": "Conectar", "pair.title": "Vincular este televisor", "pair.body": "Abre {url} en otro dispositivo e introduce este código.", "pair.waiting": "Esperando aprobación…", "profiles.title": "¿Quién está mirando?", "pin.title": "Introduce el PIN de {name}", "pin.submit": "Desbloquear perfil", "nav.home": "Inicio", "nav.search": "Buscar", "nav.library": "Biblioteca", "nav.calendar": "Calendario", "nav.settings": "Ajustes", "home.continue": "Seguir viendo", "search.placeholder": "Películas, series y TV en directo", "search.empty": "No hay resultados.", "library.empty": "Tu biblioteca está vacía.", "calendar.empty": "No hay próximos estrenos.", "media.play": "Reproducir", "media.resume": "Continuar en {time}", "source.title": "Elegir una fuente", "source.empty": "No hay fuentes compatibles.", "player.buffering": "Cargando…", "player.pause": "Pausar", "player.resume": "Continuar", "player.stop": "Detener", "settings.title": "Ajustes", "settings.changeProfile": "Cambiar perfil", "settings.changeServer": "Cambiar servidor", "error.title": "Algo salió mal", "error.network": "No se pudo contactar con el servidor.", "error.playback": "No se pudo iniciar la reproducción."
};

const de: Messages = {
  ...en, "common.back": "Zurück", "common.cancel": "Abbrechen", "common.close": "Schließen", "common.loading": "Wird geladen…", "common.retry": "Erneut versuchen", "server.title": "Mit deinem Rivune-Server verbinden", "server.body": "Gib die HTTPS-Adresse deines selbst gehosteten Servers ein.", "server.address": "Serveradresse", "server.connect": "Verbinden", "pair.title": "Diesen Fernseher koppeln", "pair.body": "Öffne {url} auf einem anderen Gerät und gib diesen Code ein.", "pair.waiting": "Warten auf Bestätigung…", "profiles.title": "Wer schaut?", "pin.title": "PIN für {name} eingeben", "pin.submit": "Profil entsperren", "nav.home": "Start", "nav.search": "Suche", "nav.library": "Mediathek", "nav.calendar": "Kalender", "nav.settings": "Einstellungen", "home.continue": "Weiterschauen", "search.placeholder": "Filme, Serien und Live-TV", "search.empty": "Keine Ergebnisse.", "library.empty": "Deine Mediathek ist leer.", "calendar.empty": "Keine kommenden Veröffentlichungen.", "media.play": "Abspielen", "media.resume": "Fortsetzen bei {time}", "source.title": "Quelle auswählen", "source.empty": "Keine kompatible Quelle verfügbar.", "player.buffering": "Puffern…", "player.pause": "Pause", "player.resume": "Fortsetzen", "player.stop": "Beenden", "settings.title": "Einstellungen", "settings.changeProfile": "Profil wechseln", "settings.changeServer": "Server wechseln", "error.title": "Etwas ist schiefgelaufen", "error.network": "Der Server ist nicht erreichbar.", "error.playback": "Die Wiedergabe konnte nicht gestartet werden."
};

const it: Messages = {
  ...en, "common.back": "Indietro", "common.cancel": "Annulla", "common.close": "Chiudi", "common.loading": "Caricamento…", "common.retry": "Riprova", "server.title": "Connettiti al tuo server Rivune", "server.body": "Inserisci l’indirizzo HTTPS del server che ospiti.", "server.address": "Indirizzo del server", "server.connect": "Connetti", "pair.title": "Associa questo televisore", "pair.body": "Apri {url} su un altro dispositivo e inserisci questo codice.", "pair.waiting": "In attesa di approvazione…", "profiles.title": "Chi sta guardando?", "pin.title": "Inserisci il PIN di {name}", "pin.submit": "Sblocca profilo", "nav.home": "Home", "nav.search": "Cerca", "nav.library": "Libreria", "nav.calendar": "Calendario", "nav.settings": "Impostazioni", "home.continue": "Continua a guardare", "search.placeholder": "Film, serie e TV in diretta", "search.empty": "Nessun risultato.", "library.empty": "La libreria è vuota.", "calendar.empty": "Nessuna uscita imminente.", "media.play": "Riproduci", "media.resume": "Riprendi da {time}", "source.title": "Scegli una sorgente", "source.empty": "Nessuna sorgente compatibile.", "player.buffering": "Buffering…", "player.pause": "Pausa", "player.resume": "Riprendi", "player.stop": "Interrompi", "settings.title": "Impostazioni", "settings.changeProfile": "Cambia profilo", "settings.changeServer": "Cambia server", "error.title": "Si è verificato un errore", "error.network": "Impossibile raggiungere il server.", "error.playback": "Impossibile avviare la riproduzione."
};

const ptBR: Messages = {
  ...en, "common.back": "Voltar", "common.cancel": "Cancelar", "common.close": "Fechar", "common.loading": "Carregando…", "common.retry": "Tentar novamente", "server.title": "Conecte ao seu servidor Rivune", "server.body": "Digite o endereço HTTPS do servidor que você hospeda.", "server.address": "Endereço do servidor", "server.connect": "Conectar", "pair.title": "Parear esta TV", "pair.body": "Abra {url} em outro dispositivo e digite este código.", "pair.waiting": "Aguardando aprovação…", "profiles.title": "Quem está assistindo?", "pin.title": "Digite o PIN de {name}", "pin.submit": "Desbloquear perfil", "nav.home": "Início", "nav.search": "Buscar", "nav.library": "Biblioteca", "nav.calendar": "Calendário", "nav.settings": "Configurações", "home.continue": "Continuar assistindo", "search.placeholder": "Filmes, séries e TV ao vivo", "search.empty": "Nenhum resultado.", "library.empty": "Sua biblioteca está vazia.", "calendar.empty": "Nenhum lançamento futuro.", "media.play": "Reproduzir", "media.resume": "Continuar em {time}", "source.title": "Escolher uma fonte", "source.empty": "Nenhuma fonte compatível.", "player.buffering": "Carregando…", "player.pause": "Pausar", "player.resume": "Continuar", "player.stop": "Parar", "settings.title": "Configurações", "settings.changeProfile": "Trocar perfil", "settings.changeServer": "Trocar servidor", "error.title": "Algo deu errado", "error.network": "Não foi possível acessar o servidor.", "error.playback": "Não foi possível iniciar a reprodução."
};

const dictionaries: Record<TvLocale, Messages> = { en, fr, es, de, it, "pt-BR": ptBR };
let activeLocale: TvLocale = "en";

export function resolveLocale(value?: string | null): TvLocale {
  const normalized = value?.trim().replace("_", "-").toLowerCase() ?? "";
  if (normalized === "pt-br" || normalized === "pt") return "pt-BR";
  if (normalized.startsWith("fr")) return "fr";
  if (normalized.startsWith("es")) return "es";
  if (normalized.startsWith("de")) return "de";
  if (normalized.startsWith("it")) return "it";
  return "en";
}

export function setLocale(value?: string | null): TvLocale {
  activeLocale = resolveLocale(value);
  document.documentElement.lang = activeLocale;
  return activeLocale;
}

export function locale(): TvLocale {
  return activeLocale;
}

export function t(key: string, values: Values = {}): string {
  const template = dictionaries[activeLocale][key] ?? en[key] ?? key;
  return template.replace(/\{([A-Za-z0-9]+)\}/g, (_, name: string) => String(values[name] ?? `{${name}}`));
}
