import Cocoa

struct Installer {
    let image: URL
    let volume: URL
    let identity: NSObject

    static func isInApplications(_ app: URL, directories: [URL] = FileManager.default.urls(
        for: .applicationDirectory, in: [.localDomainMask, .userDomainMask])) -> Bool {
        let parent = app.resolvingSymlinksInPath().deletingLastPathComponent()
        return directories.contains { $0.resolvingSymlinksInPath() == parent }
    }

    static func find(for app: URL, images: [[String: Any]]) -> Installer? {
        let files = FileManager.default
        let infoPath = "Contents/Info.plist"
        guard let installed = NSDictionary(contentsOf: app.appendingPathComponent(infoPath)),
              let identifier = installed["CFBundleIdentifier"] as? String,
              identifier == "dev.swarmview.desktop",
              let version = installed["CFBundleShortVersionString"] as? String else { return nil }
        for entry in images {
            guard let path = entry["image-path"] as? String,
                  let entities = entry["system-entities"] as? [[String: Any]] else { continue }
            let image = URL(fileURLWithPath: path)
            guard image.pathExtension.lowercased() == "dmg",
                  let attributes = try? image.resourceValues(forKeys: [.isRegularFileKey, .fileResourceIdentifierKey]),
                  attributes.isRegularFile == true,
                  let identity = attributes.fileResourceIdentifier as? NSObject else { continue }
            for entity in entities {
                guard let mount = entity["mount-point"] as? String else { continue }
                let volume = URL(fileURLWithPath: mount)
                let source = volume.appendingPathComponent("Swarmview.app")
                guard let info = NSDictionary(contentsOf: source.appendingPathComponent(infoPath)),
                      info["CFBundleIdentifier"] as? String == identifier,
                      info["CFBundleShortVersionString"] as? String == version,
                      ["Swarmview", "swarmview-server"].allSatisfy({ name in
                          let executable = "Contents/MacOS/" + name
                          return files.contentsEqual(atPath: app.appendingPathComponent(executable).path,
                              andPath: source.appendingPathComponent(executable).path)
                      }) else { continue }
                return Installer(image: image, volume: volume, identity: identity)
            }
        }
        return nil
    }

    static func mounted(for app: URL) -> Installer? {
        let process = Process(), output = Pipe()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/hdiutil")
        process.arguments = ["info", "-plist"]
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
            try? output.fileHandleForWriting.close()
            let data = output.fileHandleForReading.readDataToEndOfFile()
            process.waitUntilExit()
            guard process.terminationStatus == 0,
                  let info = try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
                  let images = info["images"] as? [[String: Any]] else { return nil }
            return find(for: app, images: images)
        } catch { return nil }
    }

    func imageIsCurrent() -> Bool {
        var file = image
        file.removeAllCachedResourceValues()
        guard let attributes = try? file.resourceValues(forKeys: [.fileResourceIdentifierKey]),
              let current = attributes.fileResourceIdentifier as? NSObject else { return false }
        return identity.isEqual(current)
    }

    func remove(completion: @escaping (Error?) -> Void) {
        DispatchQueue.global(qos: .utility).async {
            do {
                guard imageIsCurrent() else { throw CocoaError(.fileReadUnknown) }
                try NSWorkspace.shared.unmountAndEjectDevice(at: volume)
                guard imageIsCurrent() else { throw CocoaError(.fileReadUnknown) }
                NSWorkspace.shared.recycle([image]) { moved, error in
                    completion(moved[image] != nil ? nil : error ?? CocoaError(.fileWriteUnknown))
                }
            } catch { completion(error) }
        }
    }
}

func checkInstaller() throws {
    let files = FileManager.default
    let root = files.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? files.removeItem(at: root) }
    let app = root.appendingPathComponent("Installed/Swarmview.app")
    let source = root.appendingPathComponent("Volume/Swarmview.app")
    for bundle in [app, source] {
        try files.createDirectory(at: bundle.appendingPathComponent("Contents/MacOS"), withIntermediateDirectories: true)
        let info = ["CFBundleIdentifier": "dev.swarmview.desktop", "CFBundleShortVersionString": "0.1.0"]
        let data = try PropertyListSerialization.data(fromPropertyList: info, format: .xml, options: 0)
        try data.write(to: bundle.appendingPathComponent("Contents/Info.plist"))
        for name in ["Swarmview", "swarmview-server"] {
            try Data(name.utf8).write(to: bundle.appendingPathComponent("Contents/MacOS/" + name))
        }
    }
    let applications = [app.deletingLastPathComponent()]
    precondition(Installer.isInApplications(app, directories: applications))
    precondition(!Installer.isInApplications(source, directories: applications))
    let linked = app.deletingLastPathComponent().appendingPathComponent("linked.app")
    try files.createSymbolicLink(at: linked, withDestinationURL: source)
    precondition(!Installer.isInApplications(linked, directories: applications))
    let image = root.appendingPathComponent("Installer.dmg")
    try Data().write(to: image)
    let entry: [String: Any] = ["image-path": image.path,
        "system-entities": [["mount-point": source.deletingLastPathComponent().path]]]
    let match = Installer.find(for: app, images: [entry])
    precondition(match?.image == image && match?.imageIsCurrent() == true)
    precondition(Installer.find(for: app, images: []) == nil)
    precondition(Installer.find(for: app, images: [["image-path": "/Applications/Swarmview.app"]]) == nil)
    try Data("different build".utf8).write(to: source.appendingPathComponent("Contents/MacOS/swarmview-server"))
    precondition(Installer.find(for: app, images: [entry]) == nil)
    try Data("replacement".utf8).write(to: image, options: .atomic)
    precondition(match?.imageIsCurrent() == false)
    print("SWARMVIEW_INSTALLER_CHECK passed (matching builds only; no user files moved)")
}
