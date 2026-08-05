package io.rivune.api

import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

class CategoryContractsTest {
    private val json = Json { ignoreUnknownKeys = true }
    private val requestJson = Json { explicitNulls = false }
    private val categoryId = UUID.fromString("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")

    @Test
    fun requiredCategoryAndScopeFieldsDecode() {
        val account = json.decodeFromString<Account>("""
            {
              "user":{"id":"11111111-1111-4111-8111-111111111111","username":"admin","role":"admin"},
              "session":{"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","activeProfile":null,"authorizationScope":"category","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}},
              "profiles":[{"id":"44444444-4444-4444-8444-444444444444","name":"Editor","description":"Editing profile","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"},"isChild":false,"hasPin":false,"canManage":true,"enabled":true,"availableFrom":null,"availableUntil":null,"accessStartTime":null,"accessEndTime":null,"accessTimezone":"UTC","accessible":true,"avatar":{"kind":"preset","presetId":"aurora","url":"/avatar.svg"}}]
            }
        """.trimIndent())

        assertEquals(AuthorizationScope.CATEGORY, account.session.authorizationScope)
        assertEquals(categoryId, account.session.category?.id)
        assertEquals("Studio", account.profiles.first().category.name)
        assertEquals("Editing profile", account.profiles.first().description)

        val token = json.decodeFromString<TokenPair>("""
            {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin","category":null}
        """.trimIndent())
        assertEquals(AuthorizationScope.GLOBAL_ADMIN, token.authorizationScope)
        assertNull(token.category)

        val session = json.decodeFromString<Session>("""
            {"id":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","deviceName":"Editing Pixel","platform":"android","ipAddress":null,"createdAt":"2026-08-03T10:00:00Z","lastSeenAt":"2026-08-03T11:00:00Z","current":true,"authorizationScope":"category","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"}}
        """.trimIndent())
        assertEquals(AuthorizationScope.CATEGORY, session.authorizationScope)
        assertEquals(categoryId, session.category?.id)
    }

    @Test
    fun requiredNullableResponseFieldsCannotBeOmitted() {
        assertFailsWith<SerializationException> {
            json.decodeFromString<TokenPair>("""
                {"tokenType":"Bearer","accessToken":"access","accessTokenExpiresAt":"2026-08-03T12:15:00Z","refreshToken":"refresh","refreshTokenExpiresAt":"2026-09-03T12:00:00Z","sessionId":"22222222-2222-4222-8222-222222222222","deviceId":"33333333-3333-4333-8333-333333333333","authorizationScope":"global_admin"}
            """.trimIndent())
        }
        assertFailsWith<SerializationException> {
            json.decodeFromString<Device>("""
                {"id":"33333333-3333-4333-8333-333333333333","name":"Editing Pixel","platform":"android","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":null,"icon":"briefcase"},"approvedAt":null,"lastSeenAt":null,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
            """.trimIndent())
        }
    }

    @Test
    fun authorizationDtosRejectInvalidConstructedContexts() {
        assertFailsWith<IllegalArgumentException> {
            tokenPair(AuthorizationScope.GLOBAL_ADMIN, categoryRef())
        }
        assertFailsWith<IllegalArgumentException> {
            tokenPair(AuthorizationScope.CATEGORY, null)
        }
        assertFailsWith<IllegalArgumentException> {
            accountSession(AuthorizationScope.GLOBAL_ADMIN, categoryRef())
        }
        assertFailsWith<IllegalArgumentException> {
            accountSession(AuthorizationScope.CATEGORY, null)
        }
        assertFailsWith<IllegalArgumentException> {
            session(AuthorizationScope.GLOBAL_ADMIN, categoryRef())
        }
        assertFailsWith<IllegalArgumentException> {
            session(AuthorizationScope.CATEGORY, null)
        }
        assertFailsWith<IllegalArgumentException> {
            profileSession(AuthorizationScope.GLOBAL_ADMIN, categoryRef())
        }
        assertFailsWith<IllegalArgumentException> {
            profileSession(AuthorizationScope.CATEGORY, null)
        }
    }

    @Test
    fun authorizationDtosRejectInvalidDecodedContexts() {
        assertInvalidDecodedAuthorizationContexts(
            tokenPair(AuthorizationScope.GLOBAL_ADMIN, null),
        )
        assertInvalidDecodedAuthorizationContexts(
            accountSession(AuthorizationScope.GLOBAL_ADMIN, null),
        )
        assertInvalidDecodedAuthorizationContexts(
            session(AuthorizationScope.GLOBAL_ADMIN, null),
        )
        assertInvalidDecodedAuthorizationContexts(
            profileSession(AuthorizationScope.GLOBAL_ADMIN, null),
        )
    }

    @Test
    fun validAuthorizationContextsRemainSerializableWithoutExplicitNulls() {
        listOf(
            encodeRequest(tokenPair(AuthorizationScope.GLOBAL_ADMIN, null)) to
                encodeRequest(tokenPair(AuthorizationScope.CATEGORY, categoryRef())),
            encodeRequest(accountSession(AuthorizationScope.GLOBAL_ADMIN, null)) to
                encodeRequest(accountSession(AuthorizationScope.CATEGORY, categoryRef())),
            encodeRequest(session(AuthorizationScope.GLOBAL_ADMIN, null)) to
                encodeRequest(session(AuthorizationScope.CATEGORY, categoryRef())),
            encodeRequest(profileSession(AuthorizationScope.GLOBAL_ADMIN, null)) to
                encodeRequest(profileSession(AuthorizationScope.CATEGORY, categoryRef())),
        ).forEach { (globalAdmin, category) ->
            assertEquals(
                "global_admin",
                globalAdmin.getValue("authorizationScope").jsonPrimitive.content,
            )
            assertNull(globalAdmin["category"])
            assertEquals(
                "category",
                category.getValue("authorizationScope").jsonPrimitive.content,
            )
            assertEquals(
                categoryId.toString(),
                category.getValue("category").jsonObject.getValue("id").jsonPrimitive.content,
            )
        }
    }

    @Test
    fun categoryAndDeviceContractsDecode() {
        val category = json.decodeFromString<Category>("""
            {"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","description":"Production","color":"#123ABC","icon":"briefcase","position":2,"isDefault":false,"profileCount":3,"deviceCount":4,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """.trimIndent())
        assertEquals(4L, category.deviceCount)

        val device = json.decodeFromString<Device>("""
            {"id":"33333333-3333-4333-8333-333333333333","name":"Editing Pixel","platform":"android","categoryId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","category":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","color":"#123ABC","icon":"briefcase"},"internalNote":null,"approvedAt":"2026-08-03T10:00:00Z","lastSeenAt":null,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}
        """.trimIndent())
        assertEquals(categoryId, device.categoryId)
        assertNull(device.internalNote)
    }

    @Test
    fun approvalRequestCarriesCategoryAsResourceIdentifier() {
        val encoded = requestJson.parseToJsonElement(requestJson.encodeToString(
            DeviceCodeApprovalRequest(
                userCode = "ABCD-EFGH",
                categoryId = categoryId,
                deviceName = "Living Room",
                internalNote = "Wall display",
            ),
        )).jsonObject
        assertEquals("ABCD-EFGH", encoded.getValue("userCode").jsonPrimitive.content)
        assertEquals(categoryId.toString(), encoded.getValue("categoryId").jsonPrimitive.content)
        assertEquals("Living Room", encoded.getValue("deviceName").jsonPrimitive.content)
    }

    @Test
    fun updateCategoryClientUsesPatchRouteAndExactBody() = runBlocking {
        val server = MockWebServer()
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(
            """{"name":"Rivune","serverVersion":"test","protocolVersion":19,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}""",
        ))
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(
            """{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Studio","description":null,"color":null,"icon":"briefcase","position":0,"isDefault":false,"profileCount":0,"deviceCount":0,"createdAt":"2026-08-03T10:00:00Z","updatedAt":"2026-08-03T11:00:00Z"}""",
        ))
        server.start()
        try {
            val serverUrl = server.url("/").toString()
            val client = RivuneApiClient(serverUrl, StubCredentialStore(serverUrl, fixtureToken()))
            val category = client.updateCategory(
                categoryId,
                CategoryUpdateRequest(description = PatchField.Null, icon = PatchField.Value("briefcase"), isDefault = true),
            )
            assertEquals(categoryId, category.id)

            server.takeRequest()
            val request = server.takeRequest()
            assertEquals("PATCH", request.method)
            assertEquals("/api/v1/categories/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", request.path)
            val body = json.parseToJsonElement(request.body.readUtf8()).jsonObject
            assertIs<JsonNull>(body.getValue("description"))
            assertEquals("briefcase", body.getValue("icon").jsonPrimitive.content)
            assertEquals(true, body.getValue("isDefault").jsonPrimitive.content.toBoolean())
            assertNull(body["name"])
        } finally {
            server.shutdown()
        }
    }

    private fun fixtureToken() = tokenPair(AuthorizationScope.GLOBAL_ADMIN, null)

    private fun categoryRef() = CategoryRef(
        id = categoryId,
        name = "Studio",
        color = null,
        icon = "briefcase",
    )

    private fun tokenPair(
        authorizationScope: AuthorizationScope,
        category: CategoryRef?,
    ) = TokenPair(
        tokenType = "Bearer",
        accessToken = "access",
        accessTokenExpiresAt = "2026-08-03T12:15:00Z",
        refreshToken = "refresh",
        refreshTokenExpiresAt = "2026-09-03T12:00:00Z",
        sessionId = UUID.fromString("22222222-2222-4222-8222-222222222222"),
        deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
        authorizationScope = authorizationScope,
        category = category,
    )

    private fun accountSession(
        authorizationScope: AuthorizationScope,
        category: CategoryRef?,
    ) = AccountSession(
        id = UUID.fromString("22222222-2222-4222-8222-222222222222"),
        deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
        activeProfile = null,
        authorizationScope = authorizationScope,
        category = category,
    )

    private fun session(
        authorizationScope: AuthorizationScope,
        category: CategoryRef?,
    ) = Session(
        id = UUID.fromString("22222222-2222-4222-8222-222222222222"),
        deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
        deviceName = "Editing Pixel",
        platform = "android",
        ipAddress = null,
        createdAt = "2026-08-03T10:00:00Z",
        lastSeenAt = "2026-08-03T11:00:00Z",
        current = true,
        authorizationScope = authorizationScope,
        category = category,
    )

    private fun profileSession(
        authorizationScope: AuthorizationScope,
        category: CategoryRef?,
    ) = ProfileSession(
        id = UUID.fromString("22222222-2222-4222-8222-222222222222"),
        userId = UUID.fromString("11111111-1111-4111-8111-111111111111"),
        username = "admin",
        deviceId = UUID.fromString("33333333-3333-4333-8333-333333333333"),
        deviceName = "Editing Pixel",
        platform = "android",
        ipAddress = null,
        createdAt = "2026-08-03T10:00:00Z",
        lastSeenAt = "2026-08-03T11:00:00Z",
        profileGrantExpiresAt = "2026-08-03T12:00:00Z",
        current = true,
        authorizationScope = authorizationScope,
        category = category,
    )

    private inline fun <reified T> encodeRequest(value: T) =
        requestJson.parseToJsonElement(requestJson.encodeToString(value)).jsonObject

    private inline fun <reified T> assertInvalidDecodedAuthorizationContexts(valid: T) {
        val payload = json.parseToJsonElement(json.encodeToString(valid)).jsonObject
        val encodedCategory = json.parseToJsonElement(json.encodeToString(categoryRef()))
        val globalAdminWithCategory = JsonObject(
            payload +
                ("authorizationScope" to JsonPrimitive("global_admin")) +
                ("category" to encodedCategory),
        )
        val categoryWithoutCategory = JsonObject(
            payload +
                ("authorizationScope" to JsonPrimitive("category")) +
                ("category" to JsonNull),
        )

        assertFailsWith<IllegalArgumentException> {
            json.decodeFromString<T>(globalAdminWithCategory.toString())
        }
        assertFailsWith<IllegalArgumentException> {
            json.decodeFromString<T>(categoryWithoutCategory.toString())
        }
    }
}

private class StubCredentialStore(issuer: String, token: TokenPair) : CredentialStore {
    private val credentials = StoredCredentials(issuer, token)

    override suspend fun load(issuer: String): StoredCredentials = credentials
    override suspend fun save(credentials: StoredCredentials) = Unit
    override suspend fun clear(issuer: String) = Unit
}
