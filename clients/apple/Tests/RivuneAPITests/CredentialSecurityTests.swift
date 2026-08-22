import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
import XCTest
@testable import RivuneAPI

final class CredentialSecurityTests: XCTestCase {
    func testCredentialsFromServerAAreNeitherAttachedNorRefreshedAgainstServerB() async throws {
        let serverA = URL(string: "https://server-a.test")!
        let serverB = URL(string: "https://server-b.test")!
        let store = IssuerScopedCredentialStore()
        try await store.save(StoredCredentials(tokens: fixtureToken(), profileContext: nil), for: serverA)

        let clientA = try RivuneAPIClient(
            serverURL: serverA,
            transport: CredentialSecurityTransport(),
            credentialStore: store
        )
        let restoredA = try await clientA.restoreSession()
        XCTAssertTrue(restoredA)

        let transportB = CredentialSecurityTransport()
        let clientB = try RivuneAPIClient(
            serverURL: serverB,
            transport: transportB,
            credentialStore: store
        )
        let restoredB = try await clientB.restoreSession()
        XCTAssertFalse(restoredB)

        do {
            _ = try await clientB.currentAccount()
            XCTFail("A server without issuer-matched credentials must not receive an authenticated request")
        } catch RivuneAPIError.notAuthenticated {
            // Expected: discovery may run, but the authenticated request is stopped locally.
        }

        do {
            _ = try await clientB.refreshSession()
            XCTFail("A refresh token issued by another server must not be used")
        } catch RivuneAPIError.notAuthenticated {
            // Expected: refresh is stopped before constructing a request.
        }

        let requests = transportB.recordedRequests()
        XCTAssertEqual(requests.map(\.url?.path), ["/.well-known/rivune"])
        XCTAssertTrue(requests.allSatisfy { $0.value(forHTTPHeaderField: "Authorization") == nil })
        XCTAssertFalse(requests.contains { $0.url?.path.hasSuffix("/auth/refresh") == true })
    }

    func testRemoteHTTPIsRejectedWhileLocalHTTPIsAccepted() async throws {
        let rejectedAddresses = [
            "http://rivune.example",
            "http://rivune.local",
            "http://169.254.10.20",
            "http://[2001:db8::20]",
            "http://[fe80::20]"
        ]
        for address in rejectedAddresses {
            XCTAssertThrowsError(
                try RivuneAPIClient(
                    serverURL: URL(string: address)!,
                    transport: CredentialSecurityTransport(),
                    credentialStore: IssuerScopedCredentialStore()
                )
            ) { error in
                guard case RivuneAPIError.invalidServerURL = error else {
                    return XCTFail("Expected invalidServerURL, got \(error)")
                }
            }
        }

        let downgradeTransport = CredentialSecurityTransport(apiBaseURL: "http://rivune.example/api/v1")
        let downgradeClient = try RivuneAPIClient(
            serverURL: URL(string: "https://rivune.example")!,
            transport: downgradeTransport,
            credentialStore: IssuerScopedCredentialStore()
        )
        do {
            _ = try await downgradeClient.discover()
            XCTFail("Discovery must not downgrade a remote server to HTTP")
        } catch RivuneAPIError.invalidServerURL {
            // Expected.
        }

        let loopbackTransport = CredentialSecurityTransport()
        let loopbackClient = try RivuneAPIClient(
            serverURL: URL(string: "http://127.0.0.1:8080")!,
            transport: loopbackTransport,
            credentialStore: IssuerScopedCredentialStore()
        )
        _ = try await loopbackClient.discover()
        XCTAssertEqual(loopbackTransport.recordedRequests().first?.url?.absoluteString, "http://127.0.0.1:8080/.well-known/rivune")

        let privateTransport = CredentialSecurityTransport()
        let privateClient = try RivuneAPIClient(
            serverURL: URL(string: "http://192.168.1.20:8080")!,
            transport: privateTransport,
            credentialStore: IssuerScopedCredentialStore()
        )
        _ = try await privateClient.discover()
        XCTAssertEqual(privateTransport.recordedRequests().first?.url?.absoluteString, "http://192.168.1.20:8080/.well-known/rivune")
    }

    func testDelayedLoginCannotRepersistAfterLogoutReturns() async throws {
        let server = URL(string: "https://auth-race.test")!
        let tokens = fixtureToken()
        let store = IssuerScopedCredentialStore()
        let transport = ControlledAuthTransport(
            tokens: tokens,
            delayedPaths: ["/api/v1/auth/login"]
        )
        let client = try RivuneAPIClient(serverURL: server, transport: transport, credentialStore: store)

        let login = Task {
            try await client.login(
                username: "admin",
                password: "password",
                device: LoginDevice(name: "Mac", platform: "macOS")
            )
        }
        await transport.waitUntilRequested("/api/v1/auth/login")
        try await client.logout()
        await transport.release("/api/v1/auth/login")

        do {
            _ = try await login.value
            XCTFail("A login started before logout must not commit credentials")
        } catch is CancellationError {
            // Expected.
        }
        let stored = try await store.load(for: server)
        XCTAssertNil(stored)
    }

    func testDelayedRefreshCannotRepersistAfterLogoutReturns() async throws {
        let server = URL(string: "https://refresh-race.test")!
        let original = fixtureToken()
        let refreshed = TokenPair(
            tokenType: original.tokenType,
            accessToken: "refreshed-access",
            accessTokenExpiresAt: original.accessTokenExpiresAt,
            refreshToken: "refreshed-refresh",
            refreshTokenExpiresAt: original.refreshTokenExpiresAt,
            sessionId: original.sessionId,
            deviceId: original.deviceId,
            authorizationScope: original.authorizationScope,
            category: original.category
        )
        let store = IssuerScopedCredentialStore()
        try await store.save(StoredCredentials(tokens: original, profileContext: nil), for: server)
        let transport = ControlledAuthTransport(
            tokens: refreshed,
            delayedPaths: ["/api/v1/auth/refresh"]
        )
        let client = try RivuneAPIClient(serverURL: server, transport: transport, credentialStore: store)
        let restored = try await client.restoreSession()
        XCTAssertTrue(restored)

        let refresh = Task { try await client.refreshSession() }
        await transport.waitUntilRequested("/api/v1/auth/refresh")
        try await client.logout()
        await transport.release("/api/v1/auth/refresh")

        do {
            _ = try await refresh.value
            XCTFail("A refresh started before logout must not commit credentials")
        } catch is CancellationError {
            // Expected.
        }
        let stored = try await store.load(for: server)
        XCTAssertNil(stored)
    }

    func testAuthenticatedRequestCannotReplayAcrossLogoutAndReplacementLogin() async throws {
        let server = URL(string: "https://generation-boundary.test")!
        let accountA = fixtureToken()
        let accountB = fixtureToken(accessToken: "server-b-access", refreshToken: "server-b-refresh")
        let store = IssuerScopedCredentialStore()
        try await store.save(StoredCredentials(tokens: accountA, profileContext: nil), for: server)
        let transport = GenerationReplayTransport(loginTokens: accountB)
        let client = try RivuneAPIClient(serverURL: server, transport: transport, credentialStore: store)
        let restored = try await client.restoreSession()
        XCTAssertTrue(restored)

        let staleRequest = Task {
            try await client.createCategory(CategoryCreateRequest(name: "Account A category"))
        }
        await transport.waitUntilCategoryRequested()
        try await client.logout()
        _ = try await client.login(
            username: "account-b",
            password: "password-b",
            device: LoginDevice(name: "iPhone", platform: "iOS")
        )
        await transport.releaseCategoryWithUnauthorized()

        do {
            _ = try await staleRequest.value
            XCTFail("A request issued by account A must not complete or retry as account B")
        } catch is CancellationError {
            // Expected.
        }
        let categoryRequests = await transport.categoryRequests()
        XCTAssertEqual(categoryRequests.count, 1)
        XCTAssertEqual(categoryRequests.first?.value(forHTTPHeaderField: "Authorization"), "Bearer \(accountA.accessToken)")
        XCTAssertFalse(categoryRequests.contains {
            $0.value(forHTTPHeaderField: "Authorization") == "Bearer \(accountB.accessToken)"
        })
    }

    func testLocalHTTPCarriesAuthenticationOnlyToTheConfiguredOrigin() async throws {
        let server = URL(string: "http://127.0.0.1:8080")!

        let loginTransport = CredentialSecurityTransport()
        let loginClient = try RivuneAPIClient(
            serverURL: server,
            transport: loginTransport,
            credentialStore: IssuerScopedCredentialStore()
        )
        do {
            _ = try await loginClient.login(
                username: "admin",
                password: "password",
                device: LoginDevice(name: "iPhone", platform: "iOS")
            )
            XCTFail("The fixture should reject the login after receiving it")
        } catch RivuneAPIError.server(let status, _, _) {
            XCTAssertEqual(status, 401)
        }
        XCTAssertEqual(loginTransport.recordedRequests().map(\.url?.path), ["/.well-known/rivune", "/api/v1/auth/login"])

        let authenticatedStore = IssuerScopedCredentialStore()
        let token = fixtureToken()
        try await authenticatedStore.save(StoredCredentials(tokens: token, profileContext: nil), for: server)
        let authenticatedTransport = CredentialSecurityTransport()
        let authenticatedClient = try RivuneAPIClient(
            serverURL: server,
            transport: authenticatedTransport,
            credentialStore: authenticatedStore
        )

        let restored = try await authenticatedClient.restoreSession()
        XCTAssertTrue(restored)
        do {
            _ = try await authenticatedClient.currentAccount()
            XCTFail("The fixture should reject the authenticated request after receiving it")
        } catch RivuneAPIError.server(let status, _, _) {
            XCTAssertEqual(status, 401)
        } catch RivuneAPIError.notAuthenticated {
            // The automatic refresh was also rejected after both HTTP requests reached the transport.
        }
        let authenticatedRequest = try XCTUnwrap(authenticatedTransport.recordedRequests().first {
            $0.url?.path == "/api/v1/auth/me"
        })
        XCTAssertEqual(authenticatedRequest.url?.absoluteString, "http://127.0.0.1:8080/api/v1/auth/me")
        XCTAssertEqual(authenticatedRequest.value(forHTTPHeaderField: "Authorization"), "Bearer \(token.accessToken)")

        XCTAssertThrowsError(
            try RivuneAPIClient(
                serverURL: URL(string: "http://198.51.100.1:8080")!,
                transport: CredentialSecurityTransport(),
                credentialStore: IssuerScopedCredentialStore()
            )
        ) { error in
            guard case RivuneAPIError.invalidServerURL = error else {
                return XCTFail("Expected invalidServerURL, got \(error)")
            }
        }
    }

    func testRemoteLogoutFailureDoesNotVetoLocalCredentialDeletion() async throws {
        let server = URL(string: "https://logout-failure.test")!
        let tokens = fixtureToken()
        let store = IssuerScopedCredentialStore()
        try await store.save(StoredCredentials(tokens: tokens, profileContext: nil), for: server)
        let transport = ControlledAuthTransport(tokens: tokens, logoutStatus: 500)
        let client = try RivuneAPIClient(serverURL: server, transport: transport, credentialStore: store)
        let restored = try await client.restoreSession()
        XCTAssertTrue(restored)

        do {
            try await client.logout()
            XCTFail("The remote revocation failure must still be reported")
        } catch RivuneAPIError.server(let status, _, _) {
            XCTAssertEqual(status, 500)
        }

        let stored = try await store.load(for: server)
        XCTAssertNil(stored)
        let authorization = await transport.authorizationHeader(for: "/api/v1/auth/logout")
        XCTAssertEqual(authorization, "Bearer \(tokens.accessToken)")
    }

    func testDeclaredResponseAtLimitIsAllowedAndAboveLimitIsRejectedBeforeBodyDelivery() {
        let limit = URLSessionTransport.maximumResponseBodyBytes
        let session = URLSession(configuration: .ephemeral)
        let transport = URLSessionTransport(session: session)
        let task = session.dataTask(with: URL(string: "https://response-limit.test")!)

        for (length, expectedDisposition) in [
            (limit, URLSession.ResponseDisposition.allow),
            (limit + 1, URLSession.ResponseDisposition.cancel)
        ] {
            let response = HTTPURLResponse(
                url: task.originalRequest!.url!,
                statusCode: 200,
                httpVersion: "HTTP/1.1",
                headerFields: ["Content-Length": String(length)]
            )!
            var receivedDisposition: URLSession.ResponseDisposition?

            transport.loader.urlSession(
                session,
                dataTask: task,
                didReceive: response
            ) { disposition in
                receivedDisposition = disposition
            }

            XCTAssertEqual(receivedDisposition, expectedDisposition, "length \(length)")
        }

        session.invalidateAndCancel()
    }

    func testDeclaredResponseHonorsConfiguredTransportLimit() {
        let limit = 256 * 1_024
        let session = URLSession(configuration: .ephemeral)
        let transport = URLSessionTransport(session: session, maximumResponseBodyBytes: limit)
        let task = session.dataTask(with: URL(string: "https://update-limit.test")!)
        let response = HTTPURLResponse(
            url: task.originalRequest!.url!,
            statusCode: 200,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Length": String(limit + 1)]
        )!
        var receivedDisposition: URLSession.ResponseDisposition?

        transport.loader.urlSession(
            session,
            dataTask: task,
            didReceive: response
        ) { disposition in
            receivedDisposition = disposition
        }

        XCTAssertEqual(receivedDisposition, .cancel)
        session.invalidateAndCancel()
    }

    func testChunkedDiscoveryAcceptsLimitAndCancelsAtLimitPlusOne() async throws {
        let exactBody = paddedBody(discoveryBody(), count: URLSessionTransport.maximumResponseBodyBytes)
        ChunkedURLProtocol.configure(
            .init(status: 200, prefixChunks: [exactBody])
        )
        let exactClient = try makeStreamingClient()
        _ = try await exactClient.discover()
        XCTAssertEqual(
            ChunkedURLProtocol.deliveredByteCount(),
            URLSessionTransport.maximumResponseBodyBytes
        )

        ChunkedURLProtocol.configure(
            oversizedChunkedPlan(status: 200, prefix: discoveryBody())
        )
        let oversizedClient = try makeStreamingClient()
        await assertResponseTooLarge { try await oversizedClient.discover() }
        XCTAssertEqual(
            ChunkedURLProtocol.deliveredByteCount(),
            URLSessionTransport.maximumResponseBodyBytes + 1
        )
    }

    func testChunkedErrorAcceptsLimitAndCancelsAtLimitPlusOneBeforeErrorParsing() async throws {
        let errorBody = Data(#"{"error":{"code":"controlled","message":"controlled failure"}}"#.utf8)
        ChunkedURLProtocol.configure(
            .init(
                status: 500,
                prefixChunks: [
                    paddedBody(errorBody, count: URLSessionTransport.maximumResponseBodyBytes)
                ]
            )
        )
        let exactClient = try makeStreamingClient()
        do {
            _ = try await exactClient.discover()
            XCTFail("The bounded error body must still be decoded")
        } catch RivuneAPIError.server(let status, let code, _) {
            XCTAssertEqual(status, 500)
            XCTAssertEqual(code, "controlled")
        }

        ChunkedURLProtocol.configure(
            oversizedChunkedPlan(status: 500, prefix: errorBody)
        )
        let oversizedClient = try makeStreamingClient()
        await assertResponseTooLarge { try await oversizedClient.discover() }
        XCTAssertEqual(
            ChunkedURLProtocol.deliveredByteCount(),
            URLSessionTransport.maximumResponseBodyBytes + 1
        )
    }

    func testInjectedSessionAuthenticationDelegateDecidesTLSChallengesExactlyOnce() {
        let policyDelegate = RejectingAuthenticationDelegate()
        let session = URLSession(
            configuration: .ephemeral,
            delegate: policyDelegate,
            delegateQueue: nil
        )
        let transport = URLSessionTransport(session: session)
        let challenge = serverTrustChallenge()
        let completions = ChallengeCompletionRecorder()

        XCTAssertTrue(transport.loader.delegate.authenticationDelegate === policyDelegate)

        transport.loader.delegate.urlSession(
            session,
            didReceive: challenge,
            completionHandler: completions.handler
        )
        let task = session.dataTask(with: URL(string: "https://challenge.test")!)
        transport.loader.delegate.urlSession(
            session,
            task: task,
            didReceive: challenge,
            completionHandler: completions.handler
        )

        XCTAssertEqual(policyDelegate.invocationCounts().session, 1)
        XCTAssertEqual(policyDelegate.invocationCounts().task, 1)
        XCTAssertEqual(
            completions.dispositions(),
            [.cancelAuthenticationChallenge, .cancelAuthenticationChallenge]
        )
        XCTAssertEqual(completions.credentialCount(), 0)
        session.invalidateAndCancel()
    }
    func testRedirectDecisionIsAlwaysNilExactlyOnceAndCannotBeOverriddenByInjectedDelegate() {
        let policyDelegate = RejectingAuthenticationDelegate()
        let session = URLSession(
            configuration: .ephemeral,
            delegate: policyDelegate,
            delegateQueue: nil
        )
        let transport = URLSessionTransport(session: session)
        let task = session.dataTask(with: URL(string: "https://redirect.test/original")!)

        for status in [302, 307, 308] {
            let response = HTTPURLResponse(
                url: task.originalRequest!.url!,
                statusCode: status,
                httpVersion: "HTTP/1.1",
                headerFields: ["Location": "https://redirect.test/second"]
            )!
            let redirectedRequest = URLRequest(url: URL(string: "https://redirect.test/second")!)
            let completions = RedirectCompletionRecorder()

            transport.loader.delegate.urlSession(
                session,
                task: task,
                willPerformHTTPRedirection: response,
                newRequest: redirectedRequest,
                completionHandler: completions.handler
            )

            XCTAssertEqual(completions.invocationCount(), 1, "status \(status)")
            XCTAssertEqual(completions.nonNilRequestCount(), 0, "status \(status)")
        }

        XCTAssertTrue(transport.loader.delegate.authenticationDelegate === policyDelegate)
        XCTAssertEqual(policyDelegate.redirectInvocationCount(), 0)
        session.invalidateAndCancel()
    }

    func testURLSessionTransportRejects302307And308WithoutSecondRequestOrBodyReplay() async throws {
        let originalURL = URL(string: "https://redirect.test/original")!
        let targetURL = URL(string: "https://redirect.test/second")!
        let requestBody = Data("credential-bearing-request".utf8)

        for status in [302, 307, 308] {
            RedirectURLProtocol.configure(
                status: status,
                originalURL: originalURL,
                targetURL: targetURL
            )
            let configuration = URLSessionConfiguration.ephemeral
            configuration.protocolClasses = [RedirectURLProtocol.self]
            let policyDelegate = RejectingAuthenticationDelegate()
            let session = URLSession(
                configuration: configuration,
                delegate: policyDelegate,
                delegateQueue: nil
            )
            let transport = URLSessionTransport(session: session)
            var request = URLRequest(url: originalURL)
            request.httpMethod = "POST"
            request.httpBody = requestBody

            let (_, response) = try await transport.data(for: request)

            XCTAssertEqual(response.statusCode, status)
            let requests = RedirectURLProtocol.recordedRequests()
            XCTAssertEqual(requests.count, 1, "status \(status)")
            XCTAssertEqual(requests.first?.url, originalURL, "status \(status)")
            XCTAssertEqual(requests.first?.method, "POST", "status \(status)")
            XCTAssertEqual(requests.first?.body, requestBody, "status \(status)")
            XCTAssertFalse(requests.contains { $0.url == targetURL }, "status \(status)")
            XCTAssertEqual(policyDelegate.redirectInvocationCount(), 0, "status \(status)")
            session.invalidateAndCancel()
        }
    }

    func testRejectedRedirectReachesStableServerErrorParsing() async throws {
        let originalURL = URL(string: "https://redirect.test/.well-known/rivune")!
        let targetURL = URL(string: "https://redirect.test/second")!

        for status in [302, 307, 308] {
            RedirectURLProtocol.configure(
                status: status,
                originalURL: originalURL,
                targetURL: targetURL
            )
            let configuration = URLSessionConfiguration.ephemeral
            configuration.protocolClasses = [RedirectURLProtocol.self]
            let session = URLSession(configuration: configuration)
            let client = try RivuneAPIClient(
                serverURL: URL(string: "https://redirect.test")!,
                transport: URLSessionTransport(session: session),
                credentialStore: IssuerScopedCredentialStore()
            )

            do {
                _ = try await client.discover()
                XCTFail("status \(status) must be parsed as an error")
            } catch RivuneAPIError.server(let receivedStatus, let code, _) {
                XCTAssertEqual(receivedStatus, status)
                XCTAssertEqual(code, "http_\(status)")
            } catch {
                XCTFail("Expected stable server error for status \(status), got \(error)")
            }

            let requests = RedirectURLProtocol.recordedRequests()
            XCTAssertEqual(requests.map(\.url), [originalURL])
            XCTAssertFalse(requests.contains { $0.url == targetURL })
            session.invalidateAndCancel()
        }
    }


    func testSessionWithoutAuthenticationDelegateUsesSystemTLSValidation() {
        let session = URLSession(configuration: .ephemeral)
        let transport = URLSessionTransport(session: session)
        let completions = ChallengeCompletionRecorder()

        transport.loader.delegate.urlSession(
            session,
            didReceive: serverTrustChallenge(),
            completionHandler: completions.handler
        )

        XCTAssertEqual(completions.dispositions(), [.performDefaultHandling])
        XCTAssertEqual(completions.credentialCount(), 0)
        session.invalidateAndCancel()
    }

    private func makeStreamingClient() throws -> RivuneAPIClient {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ChunkedURLProtocol.self]
        let session = URLSession(configuration: configuration)
        return try RivuneAPIClient(
            serverURL: URL(string: "https://streaming.test")!,
            transport: URLSessionTransport(session: session),
            credentialStore: IssuerScopedCredentialStore()
        )
    }

    private func discoveryBody() -> Data {
        Data("""
        {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
        """.utf8)
    }

    private func paddedBody(_ body: Data, count: Int) -> Data {
        precondition(body.count <= count)
        var result = body
        result.append(Data(repeating: 0x20, count: count - body.count))
        return result
    }

    private func oversizedChunkedPlan(status: Int, prefix: Data) -> ChunkedURLProtocol.Plan {
        let limit = URLSessionTransport.maximumResponseBodyBytes
        return .init(
            status: status,
            prefixChunks: [paddedBody(prefix, count: limit), Data([0x20])],
            trailingChunk: Data(repeating: 0x20, count: 1024)
        )
    }

    private func assertResponseTooLarge<T>(
        operation: () async throws -> T
    ) async {
        do {
            _ = try await operation()
            XCTFail("Expected the response-size limit to reject the body")
        } catch RivuneAPIError.responseTooLarge(let maximumBytes) {
            XCTAssertEqual(maximumBytes, URLSessionTransport.maximumResponseBodyBytes)
        } catch {
            XCTFail("Expected responseTooLarge, got \(error)")
        }
    }

    private func serverTrustChallenge() -> URLAuthenticationChallenge {
        URLAuthenticationChallenge(
            protectionSpace: URLProtectionSpace(
                host: "challenge.test",
                port: 443,
                protocol: "https",
                realm: nil,
                authenticationMethod: "NSURLAuthenticationMethodServerTrust"
            ),
            proposedCredential: nil,
            previousFailureCount: 0,
            failureResponse: nil,
            error: nil,
            sender: AuthenticationChallengeSender()
        )
    }

    private func fixtureToken(
        accessToken: String = "server-a-access",
        refreshToken: String = "server-a-refresh"
    ) -> TokenPair {
        TokenPair(
            tokenType: "Bearer",
            accessToken: accessToken,
            accessTokenExpiresAt: "2026-08-03T12:15:00Z",
            refreshToken: refreshToken,
            refreshTokenExpiresAt: "2026-09-03T12:00:00Z",
            sessionId: UUID(uuidString: "22222222-2222-4222-8222-222222222222")!,
            deviceId: UUID(uuidString: "33333333-3333-4333-8333-333333333333")!,
            authorizationScope: .globalAdministrator,
            category: nil
        )
    }
}

private actor IssuerScopedCredentialStore: CredentialStore {
    private var credentialsByIssuer: [String: StoredCredentials] = [:]

    func load(for issuer: URL) async throws -> StoredCredentials? {
        credentialsByIssuer[issuer.absoluteString]
    }

    func save(_ credentials: StoredCredentials, for issuer: URL) async throws {
        credentialsByIssuer[issuer.absoluteString] = credentials
    }

    func clear(for issuer: URL) async throws {
        credentialsByIssuer.removeValue(forKey: issuer.absoluteString)
    }
}

private final class CredentialSecurityTransport: HTTPTransport, @unchecked Sendable {
    private let lock = NSLock()
    private var requests: [URLRequest] = []
    private let apiBaseURL: String

    init(apiBaseURL: String = "/api/v1") {
        self.apiBaseURL = apiBaseURL
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        lock.withLock {
            requests.append(request)
        }
        let body = Data("""
        {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"\(apiBaseURL)","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
        """.utf8)
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: request.url?.path == "/.well-known/rivune" ? 200 : 401,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        return (body, response)
    }

    func recordedRequests() -> [URLRequest] {
        lock.withLock { requests }
    }
}

private actor GenerationReplayTransport: HTTPTransport {
    private let loginTokens: TokenPair
    private var requests: [URLRequest] = []
    private var categoryContinuation: CheckedContinuation<(Data, HTTPURLResponse), Error>?
    private var categoryWaiters: [CheckedContinuation<Void, Never>] = []

    init(loginTokens: TokenPair) {
        self.loginTokens = loginTokens
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requests.append(request)
        let path = request.url?.path ?? ""
        if path == "/api/v1/categories" {
            let categoryRequestCount = requests.lazy.filter { $0.url?.path == path }.count
            for waiter in categoryWaiters {
                waiter.resume()
            }
            categoryWaiters.removeAll()
            if categoryRequestCount == 1 {
                return try await withCheckedThrowingContinuation { continuation in
                    categoryContinuation = continuation
                }
            }
        }
        return response(for: request)
    }

    func waitUntilCategoryRequested() async {
        if requests.contains(where: { $0.url?.path == "/api/v1/categories" }) { return }
        await withCheckedContinuation { continuation in
            categoryWaiters.append(continuation)
        }
    }

    func releaseCategoryWithUnauthorized() {
        guard let continuation = categoryContinuation else { return }
        categoryContinuation = nil
        let url = URL(string: "https://generation-boundary.test/api/v1/categories")!
        let response = HTTPURLResponse(
            url: url,
            statusCode: 401,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        continuation.resume(returning: (
            Data(#"{"error":{"code":"invalid_access_token","message":"expired"}}"#.utf8),
            response
        ))
    }

    func categoryRequests() -> [URLRequest] {
        requests.filter { $0.url?.path == "/api/v1/categories" }
    }

    private func response(for request: URLRequest) -> (Data, HTTPURLResponse) {
        let path = request.url?.path ?? ""
        let status: Int
        let body: Data
        switch path {
        case "/.well-known/rivune":
            status = 200
            body = Data("""
            {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
            """.utf8)
        case "/api/v1/auth/logout":
            status = 204
            body = Data()
        case "/api/v1/auth/login":
            status = 200
            body = encoded(loginTokens)
        case "/api/v1/categories":
            status = 200
            body = Data("""
            {"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Account B category","description":null,"color":null,"icon":null,"position":0,"isDefault":false,"profileCount":0,"deviceCount":0,"createdAt":"2026-08-03T12:00:00Z","updatedAt":"2026-08-03T12:00:00Z"}
            """.utf8)
        default:
            status = 404
            body = Data()
        }
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: status,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        return (body, response)
    }

    private func encoded(_ tokens: TokenPair) -> Data {
        guard let data = try? JSONEncoder().encode(tokens),
              let jsonObject = try? JSONSerialization.jsonObject(with: data),
              var object = jsonObject as? [String: Any] else {
            return Data()
        }
        if tokens.category == nil {
            object["category"] = NSNull()
        }
        return (try? JSONSerialization.data(withJSONObject: object)) ?? Data()
    }
}

private actor ControlledAuthTransport: HTTPTransport {
    private let tokens: TokenPair
    private let delayedPaths: Set<String>
    private let logoutStatus: Int
    private var requestedPaths: [String] = []
    private var authorizationHeaders: [String: String] = [:]
    private var requestWaiters: [String: [CheckedContinuation<Void, Never>]] = [:]
    private var pendingResponses: [
        String: CheckedContinuation<(Data, HTTPURLResponse), Error>
    ] = [:]

    init(tokens: TokenPair, delayedPaths: Set<String> = [], logoutStatus: Int = 204) {
        self.tokens = tokens
        self.delayedPaths = delayedPaths
        self.logoutStatus = logoutStatus
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let path = request.url?.path ?? ""
        requestedPaths.append(path)
        authorizationHeaders[path] = request.value(forHTTPHeaderField: "Authorization")
        for waiter in requestWaiters.removeValue(forKey: path) ?? [] {
            waiter.resume()
        }

        if delayedPaths.contains(path) {
            return try await withCheckedThrowingContinuation { continuation in
                pendingResponses[path] = continuation
            }
        }
        return response(for: request)
    }

    func waitUntilRequested(_ path: String) async {
        if requestedPaths.contains(path) { return }
        await withCheckedContinuation { continuation in
            requestWaiters[path, default: []].append(continuation)
        }
    }

    func release(_ path: String) {
        guard let continuation = pendingResponses.removeValue(forKey: path) else {
            return
        }
        let request = URLRequest(url: URL(string: "https://controlled.test\(path)")!)
        continuation.resume(returning: response(for: request))
    }

    func authorizationHeader(for path: String) -> String? {
        authorizationHeaders[path]
    }

    private func response(for request: URLRequest) -> (Data, HTTPURLResponse) {
        let path = request.url?.path ?? ""
        let status: Int
        let body: Data
        switch path {
        case "/.well-known/rivune":
            status = 200
            body = Data("""
            {"name":"Rivune","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
            """.utf8)
        case "/api/v1/auth/logout":
            status = logoutStatus
            body = logoutStatus == 204
                ? Data()
                : Data(#"{"error":{"code":"logout_failed","message":"revocation failed"}}"#.utf8)
        default:
            status = 200
            body = encodedTokens()
        }
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: status,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        return (body, response)
    }

    private func encodedTokens() -> Data {
        guard let encoded = try? JSONEncoder().encode(tokens),
              let jsonObject = try? JSONSerialization.jsonObject(with: encoded),
              var object = jsonObject as? [String: Any] else {
            return Data()
        }
        if tokens.category == nil {
            object["category"] = NSNull()
        }
        return (try? JSONSerialization.data(withJSONObject: object)) ?? Data()
    }
}

private final class WeakSendableBox<Value: AnyObject>: @unchecked Sendable {
    weak var value: Value?

    init(_ value: Value) {
        self.value = value
    }
}

private final class ChunkedURLProtocol: URLProtocol {
    struct Plan: Sendable {
        let status: Int
        let headers: [String: String]
        let prefixChunks: [Data]
        let trailingChunk: Data?

        init(
            status: Int,
            headers: [String: String] = [:],
            prefixChunks: [Data],
            trailingChunk: Data? = nil
        ) {
            self.status = status
            self.headers = headers
            self.prefixChunks = prefixChunks
            self.trailingChunk = trailingChunk
        }
    }

    private static let sharedLock = NSLock()
    private static var configuredPlan: Plan?
    private static var deliveredBytes = 0

    private let stoppedLock = NSLock()
    private let stoppedSignal = DispatchSemaphore(value: 0)
    private var stopped = false

    static func configure(_ plan: Plan) {
        sharedLock.withLock {
            configuredPlan = plan
            deliveredBytes = 0
        }
    }

    static func deliveredByteCount() -> Int {
        sharedLock.withLock { deliveredBytes }
    }

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        let plan = Self.sharedLock.withLock { Self.configuredPlan }
        guard let plan else {
            client?.urlProtocol(self, didFailWithError: RivuneAPIError.invalidResponse)
            return
        }

        let protocolBox = WeakSendableBox(self)
        DispatchQueue.global().async {
            guard let self = protocolBox.value, !self.isStopped else { return }
            let response = HTTPURLResponse(
                url: self.request.url!,
                statusCode: plan.status,
                httpVersion: "HTTP/1.1",
                headerFields: plan.headers
            )!
            self.client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)

            for chunk in plan.prefixChunks {
                guard !self.isStopped else { return }
                Self.sharedLock.withLock {
                    Self.deliveredBytes += chunk.count
                }
                self.client?.urlProtocol(self, didLoad: chunk)
            }

            if let trailingChunk = plan.trailingChunk {
                _ = self.stoppedSignal.wait(timeout: .now() + 2)
                guard !self.isStopped else { return }
                Self.sharedLock.withLock {
                    Self.deliveredBytes += trailingChunk.count
                }
                self.client?.urlProtocol(self, didLoad: trailingChunk)
            }
            guard !self.isStopped else { return }
            self.client?.urlProtocolDidFinishLoading(self)
        }
    }

    override func stopLoading() {
        stoppedLock.withLock {
            stopped = true
        }
        stoppedSignal.signal()
    }

    private var isStopped: Bool {
        stoppedLock.withLock { stopped }
    }
}


private final class RedirectURLProtocol: URLProtocol {
    struct RecordedRequest: Sendable {
        let url: URL?
        let method: String?
        let body: Data?
    }

    private struct Plan: Sendable {
        let status: Int
        let originalURL: URL
        let targetURL: URL
    }

    private static let sharedLock = NSLock()
    private static var plan: Plan?
    private static var requests: [RecordedRequest] = []

    static func configure(status: Int, originalURL: URL, targetURL: URL) {
        sharedLock.withLock {
            plan = Plan(status: status, originalURL: originalURL, targetURL: targetURL)
            requests = []
        }
    }

    static func recordedRequests() -> [RecordedRequest] {
        sharedLock.withLock { requests }
    }
    private static func bodyData(for request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }

        var body = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while true {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count < 0 { return nil }
            if count == 0 { return body }
            body.append(contentsOf: buffer[..<count])
        }
    }


    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        guard let plan = Self.sharedLock.withLock({ Self.plan }) else {
            client?.urlProtocol(self, didFailWithError: RivuneAPIError.invalidResponse)
            return
        }
        let requestBody = Self.bodyData(for: request)
        Self.sharedLock.withLock {
            Self.requests.append(
                RecordedRequest(
                    url: request.url,
                    method: request.httpMethod,
                    body: requestBody
                )
            )
        }

        guard request.url == plan.originalURL else {
            let body = Data("""
            {"name":"Unsafe redirect","serverVersion":"test","protocolVersion":20,"apiBaseUrl":"/api/v1","setupRequired":false,"timezone":"UTC","interfaceLanguage":"en"}
            """.utf8)
            let response = HTTPURLResponse(
                url: request.url!,
                statusCode: 200,
                httpVersion: "HTTP/1.1",
                headerFields: ["Content-Length": String(body.count)]
            )!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: body)
            client?.urlProtocolDidFinishLoading(self)
            return
        }

        let response = HTTPURLResponse(
            url: plan.originalURL,
            statusCode: plan.status,
            httpVersion: "HTTP/1.1",
            headerFields: ["Location": plan.targetURL.absoluteString]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}


private final class RejectingAuthenticationDelegate: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var sessionInvocations = 0
    private var taskInvocations = 0
    private var redirectInvocations = 0

    func urlSession(
        _ session: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        lock.withLock {
            sessionInvocations += 1
        }
        completionHandler(.cancelAuthenticationChallenge, nil)
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        lock.withLock {
            taskInvocations += 1
        }
        completionHandler(.cancelAuthenticationChallenge, nil)
    }
    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping @Sendable (URLRequest?) -> Void
    ) {
        lock.withLock {
            redirectInvocations += 1
        }
        completionHandler(request)
    }


    func invocationCounts() -> (session: Int, task: Int) {
        lock.withLock { (sessionInvocations, taskInvocations) }
    }

    func redirectInvocationCount() -> Int {
        lock.withLock { redirectInvocations }
    }
}

private final class RedirectCompletionRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var invocationTotal = 0
    private var nonNilRequestTotal = 0

    var handler: @Sendable (URLRequest?) -> Void {
        { [self] request in
            lock.withLock {
                invocationTotal += 1
                if request != nil {
                    nonNilRequestTotal += 1
                }
            }
        }
    }

    func invocationCount() -> Int {
        lock.withLock { invocationTotal }
    }

    func nonNilRequestCount() -> Int {
        lock.withLock { nonNilRequestTotal }
    }
}


private final class ChallengeCompletionRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var receivedDispositions: [URLSession.AuthChallengeDisposition] = []
    private var receivedCredentialCount = 0

    var handler: @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void {
        { [self] disposition, credential in
            record(disposition, credential)
        }
    }

    func record(_ disposition: URLSession.AuthChallengeDisposition, _ credential: URLCredential?) {
        lock.withLock {
            receivedDispositions.append(disposition)
            if credential != nil {
                receivedCredentialCount += 1
            }
        }
    }

    func dispositions() -> [URLSession.AuthChallengeDisposition] {
        lock.withLock { receivedDispositions }
    }

    func credentialCount() -> Int {
        lock.withLock { receivedCredentialCount }
    }
}

private final class AuthenticationChallengeSender: NSObject, URLAuthenticationChallengeSender, @unchecked Sendable {
    func use(_ credential: URLCredential, for challenge: URLAuthenticationChallenge) {}
    func continueWithoutCredential(for challenge: URLAuthenticationChallenge) {}
    func cancel(_ challenge: URLAuthenticationChallenge) {}
    func performDefaultHandling(for challenge: URLAuthenticationChallenge) {}
    func rejectProtectionSpaceAndContinue(with challenge: URLAuthenticationChallenge) {}
}