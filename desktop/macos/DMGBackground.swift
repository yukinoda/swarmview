import Cocoa

// Rasterize the approved Figma vector artwork at Retina resolution, at build time only.
let width = 760, height = 480, scale = 2
guard CommandLine.arguments.count == 3,
      let artwork = NSImage(contentsOfFile: CommandLine.arguments[1]),
      let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil,
        pixelsWide: width * scale, pixelsHigh: height * scale,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { exit(1) }
precondition(artwork.size == NSSize(width: width, height: height), "Unexpected installer canvas size")
bitmap.size = NSSize(width: width, height: height)
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
artwork.draw(in: NSRect(x: 0, y: 0, width: width, height: height))
NSGraphicsContext.restoreGraphicsState()
let pixels = bitmap.bitmapData!
precondition(Array(UnsafeBufferPointer(start: pixels, count: 4)) == [255, 255, 255, 255],
    "Artwork and Finder's fallback color must match")
func isBackground(_ x: Int, _ y: Int) -> Bool {
    let offset = y * bitmap.bytesPerRow + x * 4
    return (0..<4).allSatisfy { pixels[offset + $0] == pixels[$0] }
}
precondition((420 * scale..<height * scale).allSatisfy { y in
    (0..<width * scale).allSatisfy { isBackground($0, y) }
}, "Installer artwork must fit below Finder's title and tab bars")
precondition((382 * scale..<402 * scale).contains { y in
    (40 * scale..<720 * scale).contains { !isBackground($0, y) }
}, "Installer footer is missing")
let output = bitmap.retagging(with: .sRGB)!
output.size = bitmap.size
try output.representation(using: .png, properties: [:])!
    .write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
