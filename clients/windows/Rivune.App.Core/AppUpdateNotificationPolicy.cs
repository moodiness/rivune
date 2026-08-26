namespace Rivune.App;

internal static class AppUpdateNotificationPolicy
{
    internal static bool ShouldPresent(string? lastPresentedVersion, string candidateVersion)
    {
        try
        {
            return lastPresentedVersion is null ||
                   AppUpdateChecker.CompareSemanticVersions(candidateVersion, lastPresentedVersion) > 0;
        }
        catch (InvalidOperationException)
        {
            return false;
        }
    }
}
