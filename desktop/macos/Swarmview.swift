import Cocoa
import WebKit
import Darwin

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    private let home = URL(string: "http://127.0.0.1:4778/")!
    private let backend = Process()
    private let lifetime = Pipe()
    private let output = Pipe()
    private var window: NSWindow!
    private var webView: WKWebView!
    private var titleObserver: NSKeyValueObservation?
    private var startupTimeout: DispatchWorkItem?
    private var log = ""
    private var ready = false
    private var quitting = false
    private var lock: Int32 = -1
    private let smoke = CommandLine.arguments.contains("--smoke-test")
    private var checkedInstaller = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        do {
            // Retain the existing storage path so upgrades keep WebKit data and installer preferences.
            let directory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
                .appendingPathComponent("Swarmview", isDirectory: true)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            lock = Darwin.open(directory.appendingPathComponent("desktop.lock").path, O_CREAT | O_RDWR | O_CLOEXEC, 0o600)
            guard lock >= 0 else { throw CocoaError(.fileWriteUnknown) }
            guard flock(lock, LOCK_EX | LOCK_NB) == 0 else {
                for app in NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "")
                    where app.processIdentifier != getpid() {
                    app.activate(options: [.activateIgnoringOtherApps])
                }
                NSApp.terminate(nil)
                return
            }
            installMenu()
            let configuration = WKWebViewConfiguration()
            webView = WKWebView(frame: .zero, configuration: configuration)
            webView.navigationDelegate = self
            webView.uiDelegate = self
            window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 680, height: 820),
                styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
            window.title = "Swarmview"
            window.minSize = NSSize(width: 380, height: 400)
            window.contentView = webView
            window.delegate = self
            window.isReleasedWhenClosed = false
            window.center()
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            titleObserver = webView.observe(\.title, options: [.new]) { [weak self] view, _ in
                self?.window.title = view.title ?? "Swarmview"
            }
            try startBackend()
        } catch { fail("Could not start Swarmview: \(error.localizedDescription)") }
    }

    private func installMenu() {
        let menu = NSMenu()
        let app = NSMenuItem()
        app.submenu = NSMenu()
        app.submenu!.addItem(withTitle: "Quit Swarmview", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        menu.addItem(app)
        let edit = NSMenuItem(title: "Edit", action: nil, keyEquivalent: "")
        edit.submenu = NSMenu(title: "Edit")
        for (title, action, key) in [("Undo", "undo:", "z"), ("Cut", "cut:", "x"),
            ("Copy", "copy:", "c"), ("Paste", "paste:", "v"), ("Select All", "selectAll:", "a")] {
            edit.submenu!.addItem(withTitle: title, action: Selector(action), keyEquivalent: key)
        }
        menu.addItem(edit)
        let help = NSMenuItem(title: "Help", action: nil, keyEquivalent: "")
        help.submenu = NSMenu(title: "Help")
        let installation = help.submenu!.addItem(withTitle: "Installation Help", action: #selector(installationHelp), keyEquivalent: "")
        installation.target = self
        menu.addItem(help)
        NSApp.mainMenu = menu
    }

    @objc private func installationHelp() {
        if let url = Bundle.main.url(forResource: "Installation", withExtension: "txt") {
            NSWorkspace.shared.open(url)
        }
    }

    private func offerInstallerCleanup() {
        guard !checkedInstaller else { return }
        checkedInstaller = true
        let app = Bundle.main.bundleURL.resolvingSymlinksInPath()
        guard Installer.isInApplications(app) else { return }
        Task { @MainActor in
            let installer = await Task.detached(priority: .utility) { Installer.mounted(for: app) }.value
            guard let installer, !quitting else { return }
            let preference = "installer.lastCleanupOffer"
            guard UserDefaults.standard.string(forKey: preference) != installer.image.path else { return }
            UserDefaults.standard.set(installer.image.path, forKey: preference)
            let alert = NSAlert()
            alert.messageText = "Swarmview is installed"
            alert.informativeText = "You can now eject the installer disk and move “\(installer.image.lastPathComponent)” to Trash. The app in Applications will stay installed."
            alert.addButton(withTitle: "Keep Installer")
            alert.addButton(withTitle: "Eject & Move to Trash")
            alert.beginSheetModal(for: window) { response in
                guard response == .alertSecondButtonReturn else { return }
                installer.remove { error in
                    guard let error else { return }
                    DispatchQueue.main.async {
                        let failure = NSAlert()
                        failure.messageText = "Installer cleanup couldn't finish"
                        failure.informativeText = "\(error.localizedDescription) You can eject the disk and move the download to Trash in Finder."
                        failure.beginSheetModal(for: self.window)
                    }
                }
            }
        }
    }

    private func startBackend() throws {
        backend.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/swarmview-server")
        backend.arguments = ["--desktop"]
        var environment = ProcessInfo.processInfo.environment
        environment["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        environment.removeValue(forKey: "TMUX")
        backend.environment = environment
        backend.currentDirectoryURL = FileManager.default.homeDirectoryForCurrentUser
        backend.standardInput = lifetime
        backend.standardOutput = output
        backend.standardError = output
        try backend.run()
        try? output.fileHandleForWriting.close()
        let reader = output.fileHandleForReading
        let process = backend
        DispatchQueue.global(qos: .utility).async { [weak self] in
            while true {
                let data = reader.availableData
                if data.isEmpty { break }
                let text = String(decoding: data, as: UTF8.self)
                DispatchQueue.main.async { self?.received(text) }
            }
            process.waitUntilExit()
            DispatchQueue.main.async {
                guard let self, !self.quitting else { return }
                self.fail("The bundled server stopped (status \(process.terminationStatus)).\n\(self.log)")
            }
        }
        let timeout = DispatchWorkItem { [weak self] in self?.fail("The bundled server did not become ready within 10 seconds.") }
        startupTimeout = timeout
        DispatchQueue.main.asyncAfter(deadline: .now() + 10, execute: timeout)
    }

    private func received(_ text: String) {
        log = String((log + text).suffix(8192))
        guard !ready, log.components(separatedBy: .newlines).contains(String(home.absoluteString.dropLast())) else { return }
        ready = true
        startupTimeout?.cancel()
        webView.load(URLRequest(url: home))
    }

    private func fail(_ message: String) {
        guard !quitting else { return }
        if smoke {
            FileHandle.standardError.write(Data((message + "\n").utf8))
            applicationWillTerminate(Notification(name: NSApplication.willTerminateNotification))
            exit(1)
        } else {
            let alert = NSAlert()
            alert.messageText = "Swarmview could not continue"
            alert.informativeText = message
            alert.runModal()
        }
        NSApp.terminate(nil)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        window?.deminiaturize(nil)
        window?.makeKeyAndOrderFront(nil)
        return true
    }

    func applicationWillTerminate(_ notification: Notification) {
        quitting = true
        startupTimeout?.cancel()
        webView?.stopLoading()
        try? lifetime.fileHandleForWriting.close()
        if backend.isRunning {
            let deadline = Date().addingTimeInterval(1)
            while backend.isRunning && Date() < deadline { Thread.sleep(forTimeInterval: 0.01) }
            if backend.isRunning { backend.terminate() }
            let killDeadline = Date().addingTimeInterval(1)
            while backend.isRunning && Date() < killDeadline { Thread.sleep(forTimeInterval: 0.01) }
            if backend.isRunning { Darwin.kill(backend.processIdentifier, SIGKILL) }
        }
        if lock >= 0 { Darwin.close(lock) }
    }

    func webView(_ view: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if action.request.url == home, action.targetFrame?.isMainFrame == true {
            decisionHandler(.allow)
        } else {
            if action.navigationType == .linkActivated, let url = action.request.url,
               ["http", "https"].contains(url.scheme?.lowercased() ?? ""), url.host != home.host {
                NSWorkspace.shared.open(url)
            }
            decisionHandler(.cancel)
        }
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? { nil }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        if (error as NSError).code == NSURLErrorCancelled { return }
        fail("Could not load the dashboard: \(error.localizedDescription)")
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { webView.reload() }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard smoke else { offerInstallerCleanup(); return }
        webView.callAsyncJavaScript("""
            const assert = (ok, message) => { if (!ok) throw Error(message); };
            const response = await fetch('/agents', { signal: AbortSignal.timeout(5000) });
            assert(response.ok, 'same-origin agent request');
            const data = await response.json();
            assert(Array.isArray(data.agents), 'agent response');
            localStorage.setItem('swarmview.smoke', 'ok');
            assert(localStorage.getItem('swarmview.smoke') === 'ok', 'persistent storage');
            localStorage.removeItem('swarmview.smoke');
            const toggle = document.getElementById('search-toggle');
            toggle.focus(); toggle.click();
            assert(document.getElementById('search-panel').open, 'search dialog');
            const search = document.getElementById('search');
            search.value = 'agent:claude'; search.dispatchEvent(new Event('input'));
            document.getElementById('close-search').click();
            assert(document.activeElement === toggle, 'search restores keyboard focus');
            document.dispatchEvent(new KeyboardEvent('keydown', {key:'k', metaKey:true, bubbles:true}));
            assert(document.getElementById('search-panel').open, 'Command K');
            document.getElementById('close-search').click();
            const focus = await fetch('/focus', {method:'POST', headers:{'content-type':'application/json'}, body:JSON.stringify({pid:-1})});
            assert(focus.status === 404, 'focus origin accepted, invalid target rejected');
            assert(CSS.supports('color', 'color-mix(in srgb, red, blue)'), 'color-mix');
            assert(CSS.supports('selector(:has(*))'), ':has');
            window.location.href = 'https://example.invalid/';
            await new Promise(resolve => setTimeout(resolve, 100));
            assert(location.href === 'http://127.0.0.1:4778/', 'external navigation blocked');
            return {passed:true, notifications:typeof Notification !== 'undefined'};
            """, arguments: [:], in: nil, in: .page) { [weak self] result in
                switch result {
                case .success:
                    self?.checkEscape()
                case .failure(let error): self?.fail("WKWebView smoke test failed: \(error)")
                }
            }
    }

    private func checkEscape() {
        Task { @MainActor in
            do {
                try await webView.evaluateJavaScript("""
                    document.getElementById('search-toggle').click();
                    document.getElementById('search').value = 'escape regression';
                    """)
                try await Task.sleep(nanoseconds: 100_000_000)
                for type in [NSEvent.EventType.keyDown, .keyUp] {
                    let event = NSEvent.keyEvent(with: type, location: .zero, modifierFlags: [],
                        timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber,
                        context: nil, characters: "\u{1b}", charactersIgnoringModifiers: "\u{1b}",
                        isARepeat: false, keyCode: 53)!
                    NSApp.sendEvent(event)
                }
                try await Task.sleep(nanoseconds: 200_000_000)
                try await webView.evaluateJavaScript("""
                    (() => {
                        if (document.getElementById('search-panel').open ||
                            document.getElementById('search').value !== '' ||
                            document.activeElement !== document.getElementById('search-toggle'))
                            throw Error('Native Escape did not close search, clear the query, and restore focus');
                        return true;
                    })()
                    """)
                print("SWARMVIEW_SMOKE passed (including native Escape)")
                NSApp.terminate(nil)
            } catch { fail("Escape regression: \(error)") }
        }
    }
}

@main
struct Swarmview {
    static func main() throws {
        if CommandLine.arguments.contains("--installer-self-test") {
            try checkInstaller()
            return
        }
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        withExtendedLifetime(delegate) { app.run() }
    }
}
