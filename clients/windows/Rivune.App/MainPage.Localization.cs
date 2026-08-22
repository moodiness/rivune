using System;
using System.Collections.Generic;
using System.Runtime.CompilerServices;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Rivune.App;

public sealed partial class MainPage
{
    private sealed class LocalizedPropertyState
    {
        internal string? Key;
        internal string? Rendered;
    }

    private sealed class LocalizedElementState
    {
        internal Dictionary<DependencyProperty, LocalizedPropertyState> Properties { get; } = [];
    }

    private readonly ConditionalWeakTable<DependencyObject, LocalizedElementState> _localizedElements = new();

    private void LocalizeVisualTree(DependencyObject root)
    {
        RegisterLocalizableProperties(root);
        var count = VisualTreeHelper.GetChildrenCount(root);
        for (var index = 0; index < count; index++)
            LocalizeVisualTree(VisualTreeHelper.GetChild(root, index));
    }

    private void RegisterLocalizableProperties(DependencyObject element)
    {
        if (element is TextBlock text)
            RegisterLocalizedProperty(text, TextBlock.TextProperty, () => text.Text, value => text.Text = value);

        if (element is ContentControl content)
            RegisterLocalizedProperty(
                content,
                ContentControl.ContentProperty,
                () => content.Content as string,
                value => content.Content = value);

        if (element is TextBox textBox)
            RegisterLocalizedProperty(textBox, TextBox.PlaceholderTextProperty, () => textBox.PlaceholderText, value => textBox.PlaceholderText = value);

        if (element is PasswordBox passwordBox)
            RegisterLocalizedProperty(passwordBox, PasswordBox.PlaceholderTextProperty, () => passwordBox.PlaceholderText, value => passwordBox.PlaceholderText = value);

        if (element is InfoBar infoBar)
        {
            RegisterLocalizedProperty(infoBar, InfoBar.TitleProperty, () => infoBar.Title, value => infoBar.Title = value);
            RegisterLocalizedProperty(infoBar, InfoBar.MessageProperty, () => infoBar.Message, value => infoBar.Message = value);
        }

        if (element is ContentDialog dialog)
        {
            RegisterLocalizedProperty(dialog, ContentDialog.TitleProperty, () => dialog.Title as string, value => dialog.Title = value);
            RegisterLocalizedProperty(dialog, ContentDialog.PrimaryButtonTextProperty, () => dialog.PrimaryButtonText, value => dialog.PrimaryButtonText = value);
            RegisterLocalizedProperty(dialog, ContentDialog.SecondaryButtonTextProperty, () => dialog.SecondaryButtonText, value => dialog.SecondaryButtonText = value);
            RegisterLocalizedProperty(dialog, ContentDialog.CloseButtonTextProperty, () => dialog.CloseButtonText, value => dialog.CloseButtonText = value);
        }

        RegisterLocalizedProperty(
            element,
            AutomationProperties.NameProperty,
            () => AutomationProperties.GetName(element),
            value => AutomationProperties.SetName(element, value));
        RegisterLocalizedProperty(
            element,
            ToolTipService.ToolTipProperty,
            () => ToolTipService.GetToolTip(element) as string,
            value => ToolTipService.SetToolTip(element, value));
    }

    private void RegisterLocalizedProperty(
        DependencyObject owner,
        DependencyProperty property,
        Func<string?> read,
        Action<string> write)
    {
        var elementState = _localizedElements.GetOrCreateValue(owner);
        if (!elementState.Properties.TryGetValue(property, out var state))
        {
            state = new LocalizedPropertyState();
            elementState.Properties.Add(property, state);
            owner.RegisterPropertyChangedCallback(property, (_, _) => ApplyLocalizedProperty(state, read, write));
        }
        ApplyLocalizedProperty(state, read, write);
    }

    private void ApplyLocalizedProperty(LocalizedPropertyState state, Func<string?> read, Action<string> write)
    {
        var current = read();
        if (!string.Equals(current, state.Rendered, StringComparison.Ordinal))
        {
            state.Key = current is not null && WindowsLocalization.ContainsKey(current) ? current : null;
            state.Rendered = null;
        }
        if (state.Key is null) return;

        var translated = UiText(state.Key);
        state.Rendered = translated;
        if (!string.Equals(current, translated, StringComparison.Ordinal)) write(translated);
    }

    private void LocalizeDialog(ContentDialog dialog)
    {
        if (dialog.Title is string title) dialog.Title = UiText(title);
        if (dialog.Content is string content) dialog.Content = UiText(content);
        dialog.PrimaryButtonText = UiText(dialog.PrimaryButtonText);
        dialog.SecondaryButtonText = UiText(dialog.SecondaryButtonText);
        dialog.CloseButtonText = UiText(dialog.CloseButtonText);
        if (dialog.Content is DependencyObject contentRoot) LocalizeVisualTree(contentRoot);
        LocalizeVisualTree(dialog);
    }
}
