using System.Diagnostics;
using System.Drawing;

namespace Rivune.Bootstrapper;

internal sealed class InstallForm : Form
{
    private readonly RadioButton _install = new();
    private readonly RadioButton _portable = new();
    private readonly TextBox _destination = new();
    private readonly Button _browse = new();
    private readonly CheckBox _desktopShortcut = new();
    private readonly Button _continue = new();
    private readonly Button _cancel = new();
    private readonly Label _status = new();
    private bool _working;

    internal InstallForm()
    {
        Text = "Rivune for Windows";
        StartPosition = FormStartPosition.CenterScreen;
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false;
        MinimizeBox = false;
        ClientSize = new Size(620, 470);
        BackColor = Color.FromArgb(15, 16, 21);
        ForeColor = Color.FromArgb(242, 239, 236);
        Font = new Font("Segoe UI", 9F);
        AutoScaleMode = AutoScaleMode.Dpi;
        Icon = Icon.ExtractAssociatedIcon(Environment.ProcessPath!);

        var title = new Label
        {
            Text = "Set up Rivune",
            Font = new Font("Segoe UI Semibold", 24F),
            AutoSize = true,
            Location = new Point(38, 30),
        };
        var introduction = new Label
        {
            Text = "One download for x64 and ARM64. Choose how Rivune should live on this PC.",
            ForeColor = Color.FromArgb(174, 172, 178),
            AutoSize = true,
            Location = new Point(42, 78),
        };

        var choices = new Panel
        {
            Location = new Point(38, 112),
            Size = new Size(544, 138),
            BackColor = Color.FromArgb(24, 25, 32),
            BorderStyle = BorderStyle.FixedSingle,
        };
        _install.Text = "Install for this user (recommended)";
        _install.Font = new Font("Segoe UI Semibold", 11F);
        _install.Location = new Point(20, 17);
        _install.Size = new Size(490, 26);
        _install.Checked = true;
        _install.CheckedChanged += (_, _) => UpdateDefaultDestination();
        var installDescription = new Label
        {
            Text = "Start Menu shortcut, Windows uninstaller, and automatic updates. No administrator access.",
            ForeColor = Color.FromArgb(160, 158, 165),
            Location = new Point(43, 46),
            Size = new Size(475, 34),
        };
        _portable.Text = "Extract a portable version";
        _portable.Font = new Font("Segoe UI Semibold", 11F);
        _portable.Location = new Point(20, 87);
        _portable.Size = new Size(490, 26);
        var portableDescription = new Label
        {
            Text = "No installer, registry entry, or uninstaller. Settings remain in your Windows AppData.",
            ForeColor = Color.FromArgb(160, 158, 165),
            Location = new Point(43, 113),
            Size = new Size(475, 22),
        };
        choices.Controls.AddRange([_install, installDescription, _portable, portableDescription]);

        var destinationLabel = new Label
        {
            Text = "Destination folder",
            Font = new Font("Segoe UI Semibold", 9F),
            AutoSize = true,
            Location = new Point(40, 274),
        };
        _destination.Location = new Point(42, 299);
        _destination.Size = new Size(452, 27);
        _destination.BackColor = Color.FromArgb(31, 32, 40);
        _destination.ForeColor = ForeColor;
        _destination.BorderStyle = BorderStyle.FixedSingle;
        _destination.Text = Deployment.DefaultInstallDirectory();
        _destination.ReadOnly = true;
        _browse.Text = "Browse…";
        _browse.Location = new Point(505, 296);
        _browse.Size = new Size(77, 31);
        _browse.FlatStyle = FlatStyle.Flat;
        _browse.Click += Browse;

        _browse.Enabled = false;
        _desktopShortcut.Text = "Create a desktop shortcut";
        _desktopShortcut.Location = new Point(42, 346);
        _desktopShortcut.Size = new Size(300, 24);
        _desktopShortcut.Checked = false;

        _status.Location = new Point(42, 387);
        _status.Size = new Size(330, 34);
        _status.ForeColor = Color.FromArgb(174, 172, 178);
        _status.Text = "The correct architecture will be selected automatically.";

        _cancel.Text = "Cancel";
        _cancel.Location = new Point(397, 402);
        _cancel.Size = new Size(86, 34);
        _cancel.FlatStyle = FlatStyle.Flat;
        _cancel.Click += (_, _) => Close();
        _continue.Text = "Continue";
        _continue.Location = new Point(493, 402);
        _continue.Size = new Size(89, 34);
        _continue.FlatStyle = FlatStyle.Flat;
        _continue.BackColor = Color.FromArgb(231, 139, 103);
        _continue.ForeColor = Color.FromArgb(24, 17, 14);
        _continue.Click += Continue;
        AcceptButton = _continue;
        CancelButton = _cancel;

        Controls.AddRange([
            title,
            introduction,
            choices,
            destinationLabel,
            _destination,
            _browse,
            _desktopShortcut,
            _status,
            _cancel,
            _continue,
        ]);
        FormClosing += (_, args) => { if (_working) args.Cancel = true; };
    }
    private void UpdateDefaultDestination()
    {
        _destination.Text = _install.Checked
            ? Deployment.DefaultInstallDirectory()
            : Deployment.DefaultPortableDirectory();
        _destination.ReadOnly = _install.Checked;
        _browse.Enabled = !_install.Checked && !_working;
    }

    private void Browse(object? sender, EventArgs args)
    {
        using var dialog = new FolderBrowserDialog
        {
            Description = _install.Checked ? "Choose where Rivune should be installed" : "Choose where the portable Rivune folder should be created",
            SelectedPath = _destination.Text,
            ShowNewFolderButton = true,
            UseDescriptionForTitle = true,
        };
        if (dialog.ShowDialog(this) == DialogResult.OK) _destination.Text = dialog.SelectedPath;
    }

    private async void Continue(object? sender, EventArgs args)
    {
        if (_working) return;
        string destination;
        try
        {
            destination = Path.GetFullPath(_destination.Text.Trim());
        }
        catch (Exception exception) when (exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            MessageBox.Show(this, "Choose a valid destination folder.", Text, MessageBoxButtons.OK, MessageBoxIcon.Warning);
            return;
        }

        SetWorking(true);
        try
        {
            _status.Text = _install.Checked ? "Installing Rivune…" : "Extracting the portable application…";
            var result = await Deployment.ApplyAsync(
                new DeploymentRequest(
                    _install.Checked ? DeploymentMode.Install : DeploymentMode.Portable,
                    destination,
                    _desktopShortcut.Checked),
                CancellationToken.None);
            _status.Text = "Rivune is ready.";
            if (result.Warning is not null)
                MessageBox.Show(this, result.Warning, "Rivune is ready with a warning", MessageBoxButtons.OK, MessageBoxIcon.Warning);
            try
            {
                var startInfo = new ProcessStartInfo(result.ApplicationPath) { UseShellExecute = true };
                Process.Start(startInfo);
            }
            catch (Exception exception)
            {
                MessageBox.Show(this, $"Rivune was deployed but could not be started automatically: {exception.Message}", "Rivune is ready", MessageBoxButtons.OK, MessageBoxIcon.Warning);
            }
            _working = false;
            Close();
        }
        catch (Exception exception)
        {
            _status.Text = "Rivune setup did not complete.";
            MessageBox.Show(this, exception.Message, "Could not set up Rivune", MessageBoxButtons.OK, MessageBoxIcon.Error);
            SetWorking(false);
        }
    }

    private void SetWorking(bool working)
    {
        _working = working;
        _install.Enabled = !working;
        _portable.Enabled = !working;
        _destination.Enabled = !working;
        _browse.Enabled = !working && !_install.Checked;
        _desktopShortcut.Enabled = !working;
        _continue.Enabled = !working;
        _cancel.Enabled = !working;
        UseWaitCursor = working;
    }
}
