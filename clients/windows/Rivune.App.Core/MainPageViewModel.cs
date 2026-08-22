using Rivune.Windows;

namespace Rivune.App.ViewModels;

public enum AppPhase
{
    Restoring,
    Server,
    Pairing,
    Profiles,
    Catalogue,
    Sources,
    Detail,
    Settings,
    Player,
    Closing,
}

public sealed class MainPageViewModel : IDisposable
{
    private CancellationTokenSource _generation = new();
    private AppPhase _phase = AppPhase.Restoring;
    private long _generationId;

    public AppPhase Phase
    {
        get => _phase;
        private set => _phase = value;
    }

    public long GenerationId => _generationId;
    public CancellationToken Token => _generation.Token;
    public RivuneApiClient? Client { get; set; }
    public Discovery? Discovery { get; set; }
    public Account? Account { get; set; }
    public Profile? Profile { get; set; }
    public CollectionItem? SelectedItem { get; set; }
    public TitleReference? SelectedTitle { get; set; }
    public PlaybackSourceOption? SelectedSource { get; set; }
    public PlaybackPreparation? Preparation { get; set; }
    public PlaybackSession? PlaybackSession { get; set; }
    public CoordinatedPlaybackItem? CoordinatedItem { get; set; }
    public IReadOnlyList<PlaybackDevice> PlaybackDevices { get; set; } = [];
    public PlaybackRoom? ActivePlaybackRoom { get; set; }

    public long Transition(AppPhase phase)
    {
        _generation.Cancel();
        _generation.Dispose();
        _generation = new CancellationTokenSource();
        unchecked { _generationId++; }
        Phase = phase;
        return _generationId;
    }

    public bool IsCurrent(long generation) => generation == _generationId && !_generation.IsCancellationRequested;

    public void ResetServer()
    {
        Transition(AppPhase.Server);
        Client?.Dispose();
        Client = null;
        Discovery = null;
        Account = null;
        Profile = null;
        ClearCoordination();
        ClearPlayback();
    }

    public void ClearPlayback()
    {
        SelectedItem = null;
        SelectedTitle = null;
        SelectedSource = null;
        Preparation = null;
        PlaybackSession = null;
        CoordinatedItem = null;
    }
    public void ClearCoordination()
    {
        PlaybackDevices = [];
        ActivePlaybackRoom = null;
        CoordinatedItem = null;
    }

    public void Dispose()
    {
        Transition(AppPhase.Closing);
        _generation.Dispose();
        Client?.Dispose();
    }
}
