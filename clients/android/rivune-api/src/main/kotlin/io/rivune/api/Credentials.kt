package io.rivune.api

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

@Serializable
data class StoredCredentials(
    val issuer: String,
    val tokens: TokenPair,
    val profileContext: String? = null,
)

interface CredentialStore {
    suspend fun load(issuer: String): StoredCredentials?
    suspend fun save(credentials: StoredCredentials)
    suspend fun clear(issuer: String)
}

class CredentialStoreException(message: String, cause: Throwable? = null) : Exception(message, cause)

internal data class CredentialCleanupResult(
    val credentials: TokenPair?,
    val error: Exception?,
)

internal class OrderedCredentialStore(
    private val store: CredentialStore,
) {
    private val generation = AtomicLong(0)
    private val mutationMutex = Mutex()

    suspend fun load(issuer: String, expectedGeneration: Long): StoredCredentials? =
        mutationMutex.withLock {
            if (generation.get() != expectedGeneration) throw staleAuthentication()
            val value = store.load(issuer)
            if (generation.get() != expectedGeneration) throw staleAuthentication()
            value
        }

    suspend fun save(credentials: StoredCredentials, expectedGeneration: Long): Boolean =
        mutationMutex.withLock {
            if (generation.get() != expectedGeneration) return@withLock false
            withContext(NonCancellable) { store.save(credentials) }
            generation.get() == expectedGeneration
        }

    suspend fun clear(issuer: String, expectedGeneration: Long): Boolean =
        mutationMutex.withLock {
            if (generation.get() != expectedGeneration) return@withLock false
            withContext(NonCancellable) { store.clear(issuer) }
            generation.get() == expectedGeneration
        }

    suspend fun invalidateAndClear(
        issuer: String,
        newGeneration: Long,
        capturedCredentials: TokenPair?,
    ): CredentialCleanupResult {
        generation.set(newGeneration)
        return withContext(NonCancellable) {
            mutationMutex.withLock {
                var credentials = capturedCredentials
                var firstError: Exception? = null
                if (credentials == null) {
                    try {
                        val stored = store.load(issuer)
                        credentials = stored?.takeIf { it.issuer == issuer }?.tokens
                    } catch (cause: Exception) {
                        firstError = cause
                    }
                }
                try {
                    store.clear(issuer)
                } catch (cause: Exception) {
                    if (firstError == null) firstError = cause
                }
                CredentialCleanupResult(credentials, firstError)
            }
        }
    }

    private fun staleAuthentication() =
        kotlinx.coroutines.CancellationException("Authentication state changed")
}

class AndroidKeystoreCredentialStore(
    context: Context,
    private val preferencesName: String = "rivune_api_credentials",
) : CredentialStore {
    private val preferences = context.applicationContext.getSharedPreferences(preferencesName, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }

    override suspend fun load(issuer: String): StoredCredentials? {
        discardLegacyCredentials()
        val scope = credentialScope(issuer)
        val encoded = preferences.getString(scope.preferencesKey, null) ?: return null
        return try {
            val encrypted = Base64.decode(encoded, Base64.NO_WRAP)
            require(encrypted.size > IV_LENGTH) { "Encrypted credentials are truncated" }
            val iv = encrypted.copyOfRange(0, IV_LENGTH)
            val payload = encrypted.copyOfRange(IV_LENGTH, encrypted.size)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(scope.keyAlias), GCMParameterSpec(TAG_LENGTH_BITS, iv))
            val stored = json.decodeFromString<StoredCredentials>(cipher.doFinal(payload).decodeToString())
            if (stored.issuer == issuer) stored else {
                clear(issuer)
                null
            }
        } catch (cause: Exception) {
            throw CredentialStoreException("Unable to decrypt Rivune credentials", cause)
        }
    }

    override suspend fun save(credentials: StoredCredentials) {
        discardLegacyCredentials()
        val scope = credentialScope(credentials.issuer)
        try {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey(scope.keyAlias))
            val encrypted = cipher.doFinal(json.encodeToString(credentials).encodeToByteArray())
            val value = Base64.encodeToString(cipher.iv + encrypted, Base64.NO_WRAP)
            if (!preferences.edit().putString(scope.preferencesKey, value).commit()) {
                throw CredentialStoreException("Unable to persist Rivune credentials")
            }
        } catch (cause: CredentialStoreException) {
            throw cause
        } catch (cause: Exception) {
            throw CredentialStoreException("Unable to encrypt Rivune credentials", cause)
        }
    }

    override suspend fun clear(issuer: String) {
        discardLegacyCredentials()
        if (!preferences.edit().remove(credentialScope(issuer).preferencesKey).commit()) {
            throw CredentialStoreException("Unable to clear Rivune credentials")
        }
    }

    private fun discardLegacyCredentials() {
        if (preferences.contains(LEGACY_CREDENTIALS_KEY) &&
            !preferences.edit().remove(LEGACY_CREDENTIALS_KEY).commit()
        ) {
            throw CredentialStoreException("Unable to discard legacy Rivune credentials")
        }
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        if (keyStore.containsAlias(LEGACY_KEY_ALIAS)) keyStore.deleteEntry(LEGACY_KEY_ALIAS)
    }

    private fun credentialScope(issuer: String): CredentialScope {
        val digest = MessageDigest.getInstance("SHA-256")
            .digest(issuer.encodeToByteArray())
            .joinToString(separator = "") { byte -> "%02x".format(byte.toInt() and 0xff) }
        return CredentialScope(
            preferencesKey = "$CREDENTIALS_KEY_PREFIX$digest",
            keyAlias = "$KEY_ALIAS_PREFIX$digest",
        )
    }

    private fun getOrCreateKey(keyAlias: String): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getKey(keyAlias, null) as? SecretKey)?.let { return it }

        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                keyAlias,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    private data class CredentialScope(val preferencesKey: String, val keyAlias: String)

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val CREDENTIALS_KEY_PREFIX = "credentials."
        const val KEY_ALIAS_PREFIX = "io.rivune.api.session."
        const val LEGACY_CREDENTIALS_KEY = "credentials"
        const val LEGACY_KEY_ALIAS = "io.rivune.api.session"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_LENGTH = 12
        const val TAG_LENGTH_BITS = 128
    }
}
