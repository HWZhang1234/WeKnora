"""Email parser for .eml and .msg files."""

import email
import email.policy
import io
import logging
import re

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser

logger = logging.getLogger(__name__)

# Lines containing these patterns are considered noise and filtered out
_NOISE_PATTERNS = [
    re.compile(r"This technical data may", re.IGNORECASE),
    re.compile(r"\\\\snowcone\\", re.IGNORECASE),  # UNC paths like \\snowcone\...
    re.compile(r"\\\\harbor\\", re.IGNORECASE),
    re.compile(r"\b\d{2}:\d{2}:\d{2}\.\d{3}\b"),  # timestamps like 12:34:56.789
    re.compile(r"\b0x[0-9A-Fa-f]{8,}\b"),  # long hex values like 0x0000ABCD
    re.compile(r"CAUTION:.*EXTERNAL"),  # external email disclaimers
    re.compile(r"This message is for the designated recipient", re.IGNORECASE),
    re.compile(r"If you are not the intended recipient", re.IGNORECASE),
    re.compile(r"\$B[!-~]+\$", re.IGNORECASE),  # ISO-2022-JP encoding artifacts
]


def _is_noise_line(line: str) -> bool:
    """Return True if the line is noise (disclaimer, UNC path, etc.)."""
    stripped = line.strip()
    if not stripped:
        return False  # blank lines are not noise, handled separately
    for pat in _NOISE_PATTERNS:
        if pat.search(stripped):
            return True
    return False


def _reverse_email_thread(body_text: str) -> str:
    """Reverse the email thread so the earliest message comes first.

    Splits on 'From:' lines that look like email thread separators.
    """
    # Split body into segments by "From:" header lines
    segments = re.split(r"(?m)^(?=From:\s*\n)", body_text)

    if len(segments) <= 1:
        # Try alternative pattern: "From: Name <email>" on same line
        segments = re.split(r"(?m)^(?=From:\s+\S)", body_text)

    if len(segments) <= 1:
        return body_text  # no thread detected, return as-is

    # Reverse: earliest email first
    segments.reverse()
    return "\n".join(seg.strip() for seg in segments if seg.strip())


def _filter_noise(text: str) -> str:
    """Remove noise lines from text while preserving structure."""
    lines = text.split("\n")
    filtered = [line for line in lines if not _is_noise_line(line)]
    result = "\n".join(filtered)
    # Collapse multiple blank lines after filtering
    result = re.sub(r"\n{3,}", "\n\n", result)
    return result


class EmlParser(BaseParser):
    """Parse .eml files using Python stdlib email module."""

    def parse_into_text(self, content: bytes) -> Document:
        msg = email.message_from_bytes(content, policy=email.policy.default)
        parts = []

        # Subject as heading
        subject = msg.get("Subject", "")
        if subject:
            parts.append(f"# {subject}\n")

        # Key headers
        for header in ("From", "To", "Cc", "Date"):
            val = msg.get(header)
            if val:
                parts.append(f"**{header}:** {val}")
        parts.append("")  # blank line separator

        # Body text — prefer plain text, fall back to HTML stripped of tags
        body = msg.get_body(preferencelist=("plain", "html"))
        if body:
            text = body.get_content()
            if body.get_content_type() == "text/html":
                text = _strip_html(text)
            text = _filter_noise(text)
            text = _reverse_email_thread(text)
            parts.append(text.strip())

        return Document(content="\n".join(parts))


class MsgParser(BaseParser):
    """Parse .msg (Outlook) files using extract-msg library."""

    def parse_into_text(self, content: bytes) -> Document:
        import extract_msg

        msg = extract_msg.openMsg(io.BytesIO(content))
        try:
            parts = []
            if msg.subject:
                parts.append(f"# {msg.subject}\n")
            if msg.sender:
                parts.append(f"**From:** {msg.sender}")
            if msg.to:
                parts.append(f"**To:** {msg.to}")
            if getattr(msg, "cc", None):
                parts.append(f"**Cc:** {msg.cc}")
            if msg.date:
                parts.append(f"**Date:** {msg.date}")
            parts.append("")  # blank line separator

            # Prefer plain text body, fall back to HTML body stripped of tags
            raw_body = None
            if msg.body:
                raw_body = msg.body.strip()
            elif getattr(msg, "htmlBody", None):
                html_body = msg.htmlBody
                if isinstance(html_body, bytes):
                    html_body = html_body.decode("utf-8", errors="replace")
                raw_body = _strip_html(html_body).strip()
            elif getattr(msg, "rtfBody", None):
                # RTF body as last resort — strip RTF control words
                rtf_body = msg.rtfBody
                if isinstance(rtf_body, bytes):
                    rtf_body = rtf_body.decode("utf-8", errors="replace")
                text = re.sub(r"\{\\[^}]*\}", "", rtf_body)
                text = re.sub(r"\\[a-z]+\d*\s?", "", text)
                text = re.sub(r"[{}]", "", text)
                if text.strip():
                    raw_body = text.strip()

            if raw_body:
                raw_body = _filter_noise(raw_body)
                raw_body = _reverse_email_thread(raw_body)
                parts.append(raw_body)
        finally:
            msg.close()

        return Document(content="\n".join(parts))


def _strip_html(html: str) -> str:
    """Convert HTML to readable plain text using BeautifulSoup.

    Removes style/script/comment blocks and extracts text with
    newline separators for block-level elements.
    """
    from bs4 import BeautifulSoup

    # Remove conditional comments (BeautifulSoup may not handle <!--[if ...]>)
    html = re.sub(r"<!--.*?-->", "", html, flags=re.DOTALL)

    soup = BeautifulSoup(html, "html.parser")

    # Remove style and script tags entirely
    for tag in soup(["style", "script"]):
        tag.decompose()

    # Extract text with newlines between block elements
    text = soup.get_text(separator="\n")

    # Clean up
    text = text.replace("\xa0", " ")  # non-breaking space
    text = text.replace("\r\n", "\n")
    text = re.sub(r"\n{3,}", "\n\n", text)  # collapse excess blank lines
    text = re.sub(r"[ \t]+", " ", text)  # collapse horizontal whitespace
    text = re.sub(r"(?m)^ ", "", text)  # remove leading space on each line
    return text
