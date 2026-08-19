using System.Globalization;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using Windows.UI.ViewManagement;

namespace Rivune.App;

public sealed partial class MainPage
{
    private static readonly Color AccentSurface = Color.FromArgb(0xFF, 0x0D, 0x0D, 0x0D);
    private readonly AccessibilitySettings _accessibilitySettings = new();
    private bool _accentHighContrastSubscribed;

    private void InitializeAccentPalette()
    {
        try
        {
            _accessibilitySettings.HighContrastChanged += AccessibilitySettings_HighContrastChanged;
            _accentHighContrastSubscribed = true;
        }
        catch (System.Runtime.InteropServices.COMException)
        {
            _accentHighContrastSubscribed = false;
        }
        ApplyAccentPalette();
    }

    private void DisposeAccentPalette()
    {
        if (!_accentHighContrastSubscribed) return;
        _accessibilitySettings.HighContrastChanged -= AccessibilitySettings_HighContrastChanged;
        _accentHighContrastSubscribed = false;
    }

    private void AccessibilitySettings_HighContrastChanged(AccessibilitySettings sender, object args) =>
        DispatcherQueue.TryEnqueue(() =>
        {
            if (!_closed) ApplyAccentPalette();
        });

    private void ApplyAccentPalette()
    {
        if (IsHighContrastActive()) return;
        var primary = ParseOpaqueColor(_devicePreferences.AccentColor);
        var isDefault = string.Equals(_devicePreferences.AccentColor, WindowsDevicePreferences.DefaultAccentColor, StringComparison.Ordinal);
        var container = isDefault
            ? Color.FromArgb(0xFF, 0x17, 0x24, 0x3D)
            : Composite(primary, AccentSurface, 0.20f);
        var onPrimary = isDefault
            ? Color.FromArgb(0xFF, 0x07, 0x15, 0x2E)
            : ReadableForeground(primary);
        var onPrimaryContainer = isDefault
            ? Color.FromArgb(0xFF, 0xA9, 0xC5, 0xFF)
            : ReadableForeground(container);
        var pressed = isDefault
            ? Color.FromArgb(0xFF, 0x5F, 0x8F, 0xEA)
            : Lerp(primary, Color.FromArgb(0xFF, 0, 0, 0), 0.10f);
        var hairline = isDefault
            ? Color.FromArgb(0x73, 0x77, 0xA7, 0xFF)
            : WithAlpha(Lerp(primary, Color.FromArgb(0xFF, 0xFF, 0xFF, 0xFF), 0.26f), 0.45f);

        SetAccentBrushColor("RivuneAccentBrush", primary);
        SetAccentBrushColor("RivuneAccentStrongBrush", onPrimaryContainer);
        SetAccentBrushColor("RivuneAccentPressedBrush", pressed);
        SetAccentBrushColor("RivuneAccentInkBrush", onPrimary);
        SetAccentBrushColor("RivuneAccentContainerBrush", container);
        SetAccentBrushColor("RivuneAccentHairlineBrush", hairline);
    }

    private bool IsHighContrastActive()
    {
        try { return _accessibilitySettings.HighContrast; }
        catch (System.Runtime.InteropServices.COMException) { return false; }
    }

    private static void SetAccentBrushColor(string resourceKey, Color color)
    {
        foreach (var dictionary in Application.Current.Resources.MergedDictionaries)
        {
            if (dictionary.ContainsKey(resourceKey) && dictionary[resourceKey] is SolidColorBrush brush)
            {
                brush.Color = color;
                return;
            }
        }
    }

    private static Color ParseOpaqueColor(string value) => Color.FromArgb(
        0xFF,
        byte.Parse(value.AsSpan(1, 2), NumberStyles.HexNumber, CultureInfo.InvariantCulture),
        byte.Parse(value.AsSpan(3, 2), NumberStyles.HexNumber, CultureInfo.InvariantCulture),
        byte.Parse(value.AsSpan(5, 2), NumberStyles.HexNumber, CultureInfo.InvariantCulture));

    private static Color Composite(Color foreground, Color background, float foregroundAlpha) => Color.FromArgb(
        0xFF,
        BlendChannel(background.R, foreground.R, foregroundAlpha),
        BlendChannel(background.G, foreground.G, foregroundAlpha),
        BlendChannel(background.B, foreground.B, foregroundAlpha));

    private static Color Lerp(Color start, Color stop, double amount)
    {
        var startLab = ToOklab(start);
        var stopLab = ToOklab(stop);
        return FromOklab(new Oklab(
            LerpComponent(startLab.L, stopLab.L, amount),
            LerpComponent(startLab.A, stopLab.A, amount),
            LerpComponent(startLab.B, stopLab.B, amount)),
            BlendChannel(start.A, stop.A, (float)amount));
    }

    private static Oklab ToOklab(Color color)
    {
        var red = LinearChannel(color.R);
        var green = LinearChannel(color.G);
        var blue = LinearChannel(color.B);
        var l = Math.Cbrt((0.4122214708 * red) + (0.5363325363 * green) + (0.0514459929 * blue));
        var m = Math.Cbrt((0.2119034982 * red) + (0.6806995451 * green) + (0.1073969566 * blue));
        var s = Math.Cbrt((0.0883024619 * red) + (0.2817188376 * green) + (0.6299787005 * blue));
        return new Oklab(
            (0.2104542553 * l) + (0.7936177850 * m) - (0.0040720468 * s),
            (1.9779984951 * l) - (2.4285922050 * m) + (0.4505937099 * s),
            (0.0259040371 * l) + (0.7827717662 * m) - (0.8086757660 * s));
    }

    private static Color FromOklab(Oklab color, byte alpha)
    {
        var l = Math.Pow(color.L + (0.3963377774 * color.A) + (0.2158037573 * color.B), 3);
        var m = Math.Pow(color.L - (0.1055613458 * color.A) - (0.0638541728 * color.B), 3);
        var s = Math.Pow(color.L - (0.0894841775 * color.A) - (1.2914855480 * color.B), 3);
        return Color.FromArgb(
            alpha,
            EncodedChannel((4.0767416621 * l) - (3.3077115913 * m) + (0.2309699292 * s)),
            EncodedChannel((-1.2684380046 * l) + (2.6097574011 * m) - (0.3413193965 * s)),
            EncodedChannel((-0.0041960863 * l) - (0.7034186147 * m) + (1.7076147010 * s)));
    }

    private static byte EncodedChannel(double value)
    {
        var clamped = Math.Clamp(value, 0, 1);
        var encoded = clamped <= 0.0031308
            ? 12.92 * clamped
            : (1.055 * Math.Pow(clamped, 1 / 2.4)) - 0.055;
        return (byte)Math.Clamp((int)Math.Round(encoded * 255, MidpointRounding.AwayFromZero), byte.MinValue, byte.MaxValue);
    }

    private static double LerpComponent(double start, double stop, double amount) =>
        start + ((stop - start) * amount);

    private static Color WithAlpha(Color color, float alpha) =>
        Color.FromArgb(ToByte(255 * alpha), color.R, color.G, color.B);

    private static byte BlendChannel(byte start, byte stop, float amount) =>
        ToByte(start + ((stop - start) * amount));

    private static byte ToByte(float value) =>
        (byte)Math.Clamp((int)MathF.Round(value, MidpointRounding.AwayFromZero), byte.MinValue, byte.MaxValue);

    private static Color ReadableForeground(Color background)
    {
        var luminance = Luminance(background);
        var blackContrast = (luminance + 0.05) / 0.05;
        var whiteContrast = 1.05 / (luminance + 0.05);
        return blackContrast >= whiteContrast
            ? Color.FromArgb(0xFF, 0, 0, 0)
            : Color.FromArgb(0xFF, 0xFF, 0xFF, 0xFF);
    }

    private static double Luminance(Color color) =>
        (0.2126 * LinearChannel(color.R)) +
        (0.7152 * LinearChannel(color.G)) +
        (0.0722 * LinearChannel(color.B));

    private static double LinearChannel(byte channel)
    {
        var value = channel / 255.0;
        return value <= 0.04045 ? value / 12.92 : Math.Pow((value + 0.055) / 1.055, 2.4);
    }

    private readonly record struct Oklab(double L, double A, double B);
}
