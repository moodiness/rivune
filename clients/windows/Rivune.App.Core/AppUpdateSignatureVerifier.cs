using System.Diagnostics;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace Rivune.App;

internal static class AppUpdateSignatureVerifier
{
    internal const string MetadataKey = "RivuneExpectedUpdateSignerSha256";
    internal const uint RevocationCheckWholeChain = 1;
    internal const uint RevocationCheckChainExcludeRoot = 0x00000080;
    internal const uint CacheOnlyUrlRetrieval = 0x00001000;
    internal const uint AuthenticodeRevocationChecks = RevocationCheckWholeChain;
    internal const uint AuthenticodeProviderFlags = RevocationCheckChainExcludeRoot;
    private const uint NoUserInterface = 2;
    private const uint FileChoice = 1;
    private const uint StateActionVerify = 1;
    private const uint StateActionClose = 2;
    private static readonly Guid GenericVerifyV2 = new("00AAC56B-CD44-11D0-8CC2-00C04FC295EE");

    internal static string Verify(string path, string expectedVersion)
    {
        var expectedSignerSha256 = Assembly.GetEntryAssembly()?
            .GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(value => value.Key.Equals(MetadataKey, StringComparison.Ordinal))?.Value;
        Verify(
            path,
            expectedSignerSha256,
            expectedVersion,
            VerifyAuthenticodeTrust,
            ReadSignerSha256,
            ReadProductVersion);
        return NormalizeSha256(expectedSignerSha256)!;
    }

    internal static void Verify(string path, string expectedSignerSha256, string expectedVersion) =>
        Verify(
            path,
            expectedSignerSha256,
            expectedVersion,
            VerifyAuthenticodeTrust,
            ReadSignerSha256,
            ReadProductVersion);

    internal static void Verify(
        string path,
        string? expectedSignerSha256,
        string? expectedVersion,
        Func<string, bool> trustVerifier,
        Func<string, string> signerSha256Reader,
        Func<string, string?> productVersionReader)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);
        ArgumentNullException.ThrowIfNull(trustVerifier);
        ArgumentNullException.ThrowIfNull(signerSha256Reader);
        ArgumentNullException.ThrowIfNull(productVersionReader);
        var expectedSigner = NormalizeSha256(expectedSignerSha256) ??
            throw new InvalidOperationException("This Rivune build does not contain an update signer pin, so automatic updates are disabled.");
        var validatedExpectedVersion = ValidateSemanticVersion(
            expectedVersion,
            "The expected Rivune update version is invalid.");
        if (!trustVerifier(path))
            throw new InvalidOperationException("The downloaded Rivune update does not have a valid Authenticode signature and revocation status.");
        var actualSigner = NormalizeSha256(signerSha256Reader(path));
        if (actualSigner is null ||
            !CryptographicOperations.FixedTimeEquals(Convert.FromHexString(actualSigner), Convert.FromHexString(expectedSigner)))
        {
            throw new InvalidOperationException("The downloaded Rivune update was signed by an unexpected certificate.");
        }
        var actualVersion = ValidateSemanticVersion(
            productVersionReader(path),
            "The downloaded Rivune update does not contain a valid PE ProductVersion.");
        if (!actualVersion.Equals(validatedExpectedVersion, StringComparison.Ordinal))
            throw new InvalidOperationException("The downloaded Rivune update PE ProductVersion does not match the update manifest.");
    }

    internal static string? NormalizeSha256(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        Span<char> normalized = stackalloc char[64];
        var count = 0;
        foreach (var character in value)
        {
            if (character is ':' or ' ' or '-') continue;
            if (count == normalized.Length || !Uri.IsHexDigit(character)) return null;
            normalized[count++] = char.ToLowerInvariant(character);
        }
        return count == normalized.Length ? new string(normalized) : null;
    }

    private static bool VerifyAuthenticodeTrust(string path)
    {
        if (!OperatingSystem.IsWindows()) return false;
        var filePath = Marshal.StringToCoTaskMemUni(path);
        var fileInfoPointer = IntPtr.Zero;
        var trustDataPointer = IntPtr.Zero;
        try
        {
            var fileInfo = new WinTrustFileInfo
            {
                Size = (uint)Marshal.SizeOf<WinTrustFileInfo>(),
                FilePath = filePath,
            };
            fileInfoPointer = Marshal.AllocCoTaskMem(Marshal.SizeOf<WinTrustFileInfo>());
            Marshal.StructureToPtr(fileInfo, fileInfoPointer, false);
            var trustData = new WinTrustData
            {
                Size = (uint)Marshal.SizeOf<WinTrustData>(),
                UiChoice = NoUserInterface,
                RevocationChecks = AuthenticodeRevocationChecks,
                UnionChoice = FileChoice,
                FileInfo = fileInfoPointer,
                StateAction = StateActionVerify,
                ProviderFlags = AuthenticodeProviderFlags,
            };
            trustDataPointer = Marshal.AllocCoTaskMem(Marshal.SizeOf<WinTrustData>());
            Marshal.StructureToPtr(trustData, trustDataPointer, false);
            var action = GenericVerifyV2;
            var result = WinVerifyTrust(IntPtr.Zero, ref action, trustDataPointer);
            trustData = Marshal.PtrToStructure<WinTrustData>(trustDataPointer);
            trustData.StateAction = StateActionClose;
            Marshal.StructureToPtr(trustData, trustDataPointer, false);
            _ = WinVerifyTrust(IntPtr.Zero, ref action, trustDataPointer);
            return result == 0;
        }
        finally
        {
            if (trustDataPointer != IntPtr.Zero) Marshal.FreeCoTaskMem(trustDataPointer);
            if (fileInfoPointer != IntPtr.Zero) Marshal.FreeCoTaskMem(fileInfoPointer);
            Marshal.FreeCoTaskMem(filePath);
        }
    }

    private static string ReadSignerSha256(string path)
    {
#pragma warning disable SYSLIB0057
        using var certificate = new X509Certificate2(X509Certificate.CreateFromSignedFile(path));
#pragma warning restore SYSLIB0057
        return certificate.GetCertHashString(HashAlgorithmName.SHA256);
    }

    private static string? ReadProductVersion(string path) =>
        FileVersionInfo.GetVersionInfo(path).ProductVersion;

    private static string ValidateSemanticVersion(string? version, string errorMessage)
    {
        if (string.IsNullOrEmpty(version)) throw new InvalidOperationException(errorMessage);
        try
        {
            _ = AppUpdateChecker.CompareSemanticVersions(version, version);
            return version;
        }
        catch (InvalidOperationException exception)
        {
            throw new InvalidOperationException(errorMessage, exception);
        }
    }

    [DllImport("wintrust.dll", ExactSpelling = true, PreserveSig = true)]
    private static extern int WinVerifyTrust(IntPtr window, [In] ref Guid action, IntPtr trustData);

    [StructLayout(LayoutKind.Sequential)]
    private struct WinTrustFileInfo
    {
        internal uint Size;
        internal IntPtr FilePath;
        internal IntPtr FileHandle;
        internal IntPtr KnownSubject;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct WinTrustData
    {
        internal uint Size;
        internal IntPtr PolicyCallbackData;
        internal IntPtr SipClientData;
        internal uint UiChoice;
        internal uint RevocationChecks;
        internal uint UnionChoice;
        internal IntPtr FileInfo;
        internal uint StateAction;
        internal IntPtr StateData;
        internal IntPtr UrlReference;
        internal uint ProviderFlags;
        internal uint UiContext;
        internal IntPtr SignatureSettings;
    }
}
