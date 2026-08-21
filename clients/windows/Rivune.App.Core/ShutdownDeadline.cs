namespace Rivune.App;

internal static class ShutdownDeadline
{
    internal static async Task RunAsync(
        Func<CancellationToken, Task> bestEffortWork,
        TimeSpan timeout,
        Action cleanup)
    {
        ArgumentNullException.ThrowIfNull(bestEffortWork);
        ArgumentNullException.ThrowIfNull(cleanup);
        if (timeout <= TimeSpan.Zero) throw new ArgumentOutOfRangeException(nameof(timeout));

        using var cancellation = new CancellationTokenSource(timeout);
        Task? work = null;
        try
        {
            work = bestEffortWork(cancellation.Token);
            await work.WaitAsync(cancellation.Token);
        }
        catch (Exception)
        {
        }
        finally
        {
            if (work is { IsCompleted: false })
            {
                _ = work.ContinueWith(
                    static completed => _ = completed.Exception,
                    CancellationToken.None,
                    TaskContinuationOptions.OnlyOnFaulted | TaskContinuationOptions.ExecuteSynchronously,
                    TaskScheduler.Default);
            }
            cleanup();
        }
    }
}
