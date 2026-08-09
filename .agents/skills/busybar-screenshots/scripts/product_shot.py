#!/usr/bin/env python3
"""Create and verify canonical transparent BUSY Bar README product shots."""

from __future__ import annotations

import argparse
from collections import deque
from pathlib import Path
import sys

try:
    from PIL import Image, ImageDraw, ImageFilter
except ModuleNotFoundError:
    print(
        "product_shot.py: Pillow 12.2 or newer is required; in Codex Desktop, "
        "use the Python path returned by load_workspace_dependencies",
        file=sys.stderr,
    )
    raise SystemExit(2)


FULL_FRAME_SIZE = (1280, 720)
CROP_BOX = (380, 75, 900, 255)
SHOT_SIZE = (520, 180)
EXPECTED_ALPHA_BBOX = (38, 19, 482, 171)
TOP_CLEAR_ROWS = 52
MASK_BLUR_RADIUS = 0.55
CHECK_SIZE = 12


def is_background(pixel: tuple[int, int, int, int]) -> bool:
    red, green, blue, _ = pixel
    high = max(red, green, blue)
    low = min(red, green, blue)
    return high <= 38 and high - low <= 10


def exterior_mask(image: Image.Image) -> Image.Image:
    width, height = image.size
    pixels = image.load()
    exterior = bytearray(width * height)
    queue: deque[tuple[int, int]] = deque()

    def add(x: int, y: int) -> None:
        index = y * width + x
        if not exterior[index] and is_background(pixels[x, y]):
            exterior[index] = 1
            queue.append((x, y))

    for x in range(width):
        add(x, 0)
        add(x, height - 1)
    for y in range(height):
        add(0, y)
        add(width - 1, y)

    while queue:
        x, y = queue.popleft()
        for neighbor_x, neighbor_y in (
            (x - 1, y),
            (x + 1, y),
            (x, y - 1),
            (x, y + 1),
        ):
            if 0 <= neighbor_x < width and 0 <= neighbor_y < height:
                add(neighbor_x, neighbor_y)

    for y in range(min(TOP_CLEAR_ROWS, height)):
        for x in range(width):
            if is_background(pixels[x, y]):
                exterior[y * width + x] = 1

    mask = Image.new("L", image.size, 255)
    mask.putdata([0 if value else 255 for value in exterior])
    return mask.filter(ImageFilter.GaussianBlur(MASK_BLUR_RADIUS))


def verify(path: Path) -> dict[str, object]:
    with Image.open(path) as source:
        if source.format != "PNG":
            raise ValueError(f"{path}: format is {source.format}, want PNG")
        if source.mode != "RGBA":
            raise ValueError(f"{path}: mode is {source.mode}, want RGBA")
        if source.size != SHOT_SIZE:
            raise ValueError(f"{path}: size is {source.size}, want {SHOT_SIZE}")
        image = source.copy()

    alpha = image.getchannel("A")
    alpha_bbox = alpha.getbbox()
    if alpha_bbox != EXPECTED_ALPHA_BBOX:
        raise ValueError(
            f"{path}: alpha bbox is {alpha_bbox}, want {EXPECTED_ALPHA_BBOX}"
        )
    if alpha.getextrema() != (0, 255):
        raise ValueError(f"{path}: alpha must contain fully clear and opaque pixels")
    corners = (
        alpha.getpixel((0, 0)),
        alpha.getpixel((SHOT_SIZE[0] - 1, 0)),
        alpha.getpixel((0, SHOT_SIZE[1] - 1)),
        alpha.getpixel((SHOT_SIZE[0] - 1, SHOT_SIZE[1] - 1)),
    )
    if any(corners):
        raise ValueError(f"{path}: corners are not transparent: {corners}")

    histogram = alpha.histogram()
    transparent = histogram[0]
    partial = sum(histogram[1:255])
    opaque = histogram[255]
    if partial == 0:
        raise ValueError(f"{path}: alpha edge has no feathered pixels")
    return {
        "path": str(path),
        "size": image.size,
        "alpha_bbox": alpha_bbox,
        "transparent": transparent,
        "partial": partial,
        "opaque": opaque,
    }


def prepare(full_frame_path: Path, output_path: Path) -> dict[str, object]:
    with Image.open(full_frame_path) as source:
        if source.size != FULL_FRAME_SIZE:
            raise ValueError(
                f"{full_frame_path}: size is {source.size}, want {FULL_FRAME_SIZE}"
            )
        frame = source.convert("RGBA")

    shot = frame.crop(CROP_BOX)
    shot.putalpha(exterior_mask(shot))
    output_path.parent.mkdir(parents=True, exist_ok=True)
    shot.save(output_path, format="PNG", optimize=True)
    return verify(output_path)


def checkerboard(output_path: Path, input_paths: list[Path]) -> None:
    if not input_paths:
        raise ValueError("checkerboard requires at least one screenshot")
    sheet = Image.new("RGB", (SHOT_SIZE[0], SHOT_SIZE[1] * len(input_paths)), "white")
    for index, input_path in enumerate(input_paths):
        verify(input_path)
        with Image.open(input_path) as source:
            image = source.convert("RGBA")
        background = Image.new("RGBA", SHOT_SIZE, "white")
        draw = ImageDraw.Draw(background)
        for y in range(0, SHOT_SIZE[1], CHECK_SIZE):
            for x in range(0, SHOT_SIZE[0], CHECK_SIZE):
                if (x // CHECK_SIZE + y // CHECK_SIZE) % 2:
                    draw.rectangle(
                        (x, y, x + CHECK_SIZE - 1, y + CHECK_SIZE - 1),
                        fill="#d8d8d8",
                    )
        background.alpha_composite(image)
        sheet.paste(background.convert("RGB"), (0, index * SHOT_SIZE[1]))
    output_path.parent.mkdir(parents=True, exist_ok=True)
    sheet.save(output_path, format="PNG", optimize=True)


def print_stats(stats: dict[str, object]) -> None:
    fields = " ".join(f"{key}={value}" for key, value in stats.items())
    print(fields)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)

    prepare_parser = commands.add_parser("prepare", help="crop, mask, and verify a full browser frame")
    prepare_parser.add_argument("full_frame", type=Path)
    prepare_parser.add_argument("output", type=Path)

    verify_parser = commands.add_parser("verify", help="verify canonical PNG metadata and alpha")
    verify_parser.add_argument("images", nargs="+", type=Path)

    checker_parser = commands.add_parser("checkerboard", help="make a visual transparency QA sheet")
    checker_parser.add_argument("output", type=Path)
    checker_parser.add_argument("images", nargs="+", type=Path)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.command == "prepare":
            print_stats(prepare(args.full_frame, args.output))
        elif args.command == "verify":
            for path in args.images:
                print_stats(verify(path))
        else:
            checkerboard(args.output, args.images)
            print(f"checkerboard={args.output}")
    except (OSError, ValueError) as error:
        print(f"product_shot.py: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
