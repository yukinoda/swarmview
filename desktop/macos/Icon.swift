import Cocoa

guard [3, 4].contains(CommandLine.arguments.count),
      let image = NSImage(contentsOfFile: CommandLine.arguments[1]) else { exit(1) }
let disk = CommandLine.arguments.count == 4 ? NSImage(contentsOfFile: CommandLine.arguments[3]) : nil
if CommandLine.arguments.count == 4 && disk == nil { exit(1) }
let directory = URL(fileURLWithPath: CommandLine.arguments[2])
for size in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let pixels = size * scale
        let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels,
            bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
            colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
        bitmap.size = NSSize(width: size, height: size)
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
        let frame = NSRect(x: 0, y: 0, width: size, height: size)
        disk?.draw(in: frame)
        // macOS app tiles occupy 824 points of the 1024-point icon canvas.
        image.draw(in: disk == nil ? frame.insetBy(dx: Double(size) * 100 / 1024, dy: Double(size) * 100 / 1024) : NSRect(x: Double(size) * 0.24, y: Double(size) * 0.29,
            width: Double(size) * 0.52, height: Double(size) * 0.52))
        NSGraphicsContext.restoreGraphicsState()
        if disk == nil {
            precondition(bitmap.colorAt(x: pixels / 20, y: pixels / 2)!.alphaComponent == 0,
                "App icon must retain transparent macOS canvas padding")
            precondition(bitmap.colorAt(x: pixels / 4, y: pixels / 2)!.alphaComponent > 0.9,
                "App icon artwork must remain visible inside the padding")
        }
        let name = "icon_\(size)x\(size)\(scale == 2 ? "@2x" : "").png"
        try bitmap.representation(using: .png, properties: [:])!.write(to: directory.appendingPathComponent(name))
    }
}
