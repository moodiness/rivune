package io.rivune.api

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

interface CredentialStore {
    suspend fun load(): TokenPair?
    suspend fun save(credentials: TokenPair)
    suspend fun clear()
}

class CredentialStoreException(message: String, cause: Throwable? = null) : Exception(message, cause)

class AndroidKeystoreCredentialStore(
    context: Context,
    private val keyAlias: String = "io.rivune.api.session",
    preferencesName: String = "rivune_api_credentials",
) : CredentialStore {
    private val preferences = context.applicationContext.getSharedPreferences(preferencesName, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = true }

    override suspend fun load(): TokenPair? {
        val encoded = preferences.getString(CREDENTIALS_KEY, null) ?: return null
        return try {
            val encrypted = Base64.decode(encoded, Base64.NO_WRAP)
            require(encrypted.size > IV_LENGTH) { "Encrypted credentials are truncated" }
            val iv = encrypted.copyOfRange(0, IV_LENGTH)
            val payload = encrypted.copyOfRange(IV_LENGTH, encrypted.size)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(), GCMParameterSpec(TAG_LENGTH_BITS, iv))
            json.decodeFromString<TokenPair>(cipher.doFinal(payload).decodeToString())
        } catch (cause: Exception) {
            throw CredentialStoreException("Unable to decrypt Rivune credentials", cause)
        }
    }

    override suspend fun save(credentials: TokenPair) {
        try {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey())
            val encrypted = cipher.doFinal(json.encodeToString(credentials).encodeToByteArray())
            val value = Base64.encodeToString(cipher.iv + encrypted, Base64.NO_WRAP)
            if (!preferences.edit().putString(CREDENTIALS_KEY, value).commit()) {
                throw CredentialStoreException("Unable to persist Rivune credentials")
            }
        } catch (cause: CredentialStoreException) {
            throw cause
        } catch (cause: Exception) {
            throw CredentialStoreException("Unable to encrypt Rivune credentials", cause)
        }
    }

    override suspend fun clear() {
        if (!preferences.edit().remove(CREDENTIALS_KEY).commit()) {
            throw CredentialStoreException("Unable to clear Rivune credentials")
        }
    }

    private fun getOrCreateKey(): SecretKey {
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

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val CREDENTIALS_KEY = "credentials"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_LENGTH = 12
        const val TAG_LENGTH_BITS = 128
    }
}
