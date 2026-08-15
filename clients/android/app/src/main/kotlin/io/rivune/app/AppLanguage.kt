package io.rivune.app

import android.content.Context
import android.content.res.Configuration
import java.util.Locale

internal enum class AppLanguage(val preferenceValue: String, val languageTag: String?) {
    ENGLISH("en", "en"),
    FRENCH("fr", "fr"),
    SYSTEM("system", null),
    ;

    companion object {
        fun fromPreference(value: String?): AppLanguage =
            entries.firstOrNull { it.preferenceValue == value } ?: SYSTEM
    }
}

private const val APP_PREFERENCES = "app_preferences"
private const val APP_LANGUAGE = "app_language"

internal fun currentAppLanguage(context: Context): AppLanguage = AppLanguage.fromPreference(
    context.getSharedPreferences(APP_PREFERENCES, Context.MODE_PRIVATE).getString(APP_LANGUAGE, null),
)

internal fun saveAppLanguage(context: Context, language: AppLanguage): Boolean {
    if (currentAppLanguage(context) == language) return false
    context.getSharedPreferences(APP_PREFERENCES, Context.MODE_PRIVATE)
        .edit()
        .putString(APP_LANGUAGE, language.preferenceValue)
        .apply()
    return true
}

internal fun contextWithAppLanguage(context: Context): Context {
    val language = currentAppLanguage(context)
    val locale = language.languageTag?.let(Locale::forLanguageTag)
        ?: context.resources.configuration.locales.get(0)
    Locale.setDefault(locale)
    if (language == AppLanguage.SYSTEM) return context

    val configuration = Configuration(context.resources.configuration).apply {
        setLocale(locale)
        setLayoutDirection(locale)
    }
    return context.createConfigurationContext(configuration)
}
