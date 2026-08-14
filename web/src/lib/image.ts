// Turning a photo somebody picked off their disk into something small enough
// to live in a database row and travel inside a WebSocket frame.
//
// A profile picture is drawn at ~24 CSS pixels in the collaboration strip, and
// the server caps what it will store at 48 KB — a phone snapshot is 3-6 MB, so
// uploading the original would fail for reasons the user cannot see or fix.
// Downscaling in the browser makes the cap something they never meet.

export interface DownscaleOptions {
  /** Longest edge of the result, in pixels. */
  maxPx: number;
  /** Hard ceiling for the encoded data URI. Quality is stepped down until the
   *  result fits — a picture that arrives slightly softer beats one rejected. */
  maxBytes: number;
}

/**
 * downscaleImageToDataURL renders `file` into a square-cropped, downscaled
 * JPEG data URI no larger than `maxBytes`.
 *
 * Square-cropped on purpose: the strip draws circles, and a portrait squeezed
 * into a circle looks like a mistake. We take the centre square, which is where
 * a face is in practically every photo anyone picks as an avatar.
 */
export async function downscaleImageToDataURL(
  file: File,
  { maxPx, maxBytes }: DownscaleOptions,
): Promise<string> {
  const bitmap = await loadImage(file);
  const side = Math.min(bitmap.width, bitmap.height);
  const size = Math.min(maxPx, side);

  const canvas = document.createElement('canvas');
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('canvas unavailable');
  // White underneath: a transparent PNG encoded as JPEG would otherwise get a
  // black background, which is not what anyone expects of their own photo.
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(0, 0, size, size);
  ctx.drawImage(
    bitmap as CanvasImageSource,
    (bitmap.width - side) / 2,
    (bitmap.height - side) / 2,
    side,
    side,
    0,
    0,
    size,
    size,
  );

  for (const quality of [0.85, 0.7, 0.55, 0.4]) {
    const uri = canvas.toDataURL('image/jpeg', quality);
    if (uri.length <= maxBytes) return uri;
  }
  // Last resort: shrink the pixels rather than hand back something the server
  // will refuse.
  if (size > 64) {
    return downscaleImageToDataURL(file, { maxPx: Math.floor(size / 2), maxBytes });
  }
  throw new Error('image too large');
}

// createImageBitmap where available (fast, off the main thread), <img> as the
// fallback for browsers/jsdom that lack it.
async function loadImage(file: File): Promise<ImageBitmap | HTMLImageElement> {
  if (typeof createImageBitmap === 'function') {
    return createImageBitmap(file);
  }
  const url = URL.createObjectURL(file);
  try {
    return await new Promise<HTMLImageElement>((resolve, reject) => {
      const img = new Image();
      img.onload = () => resolve(img);
      img.onerror = () => reject(new Error('decode failed'));
      img.src = url;
    });
  } finally {
    URL.revokeObjectURL(url);
  }
}
