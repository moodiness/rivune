package io.rivune.app

import kotlin.test.Test
import kotlin.test.assertEquals

class AppLanguageTest {
    @Test
    fun missingOrInvalidPreferenceDefaultsToSystem() {
        assertEquals(AppLanguage.SYSTEM, AppLanguage.fromPreference(null))
        assertEquals(AppLanguage.SYSTEM, AppLanguage.fromPreference("unsupported"))
    }

    @Test
    fun supportedPreferencesRoundTrip() {
        AppLanguage.entries.forEach { language ->
            assertEquals(language, AppLanguage.fromPreference(language.preferenceValue))
        }
    }
}
