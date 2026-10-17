package io.github.erikedits.homelydrive

import java.io.BufferedOutputStream
import java.io.File
import java.io.FileOutputStream
import java.io.OutputStream

/**
 * Writes a PDF whose pages are JPEG images (scanner output). The JPEG data is
 * embedded as is (DCTDecode), which keeps scans small — Android's PdfDocument
 * would store the pictures uncompressed.
 */
class PdfWriter(file: File, private val pageCount: Int) : AutoCloseable {
    private val out = CountingStream(BufferedOutputStream(FileOutputStream(file)))
    private val objOffsets = HashMap<Int, Long>()
    private var pageIndex = 0

    init {
        out.write("%PDF-1.4\n%âãÏÓ\n".toByteArray(Charsets.ISO_8859_1))
        obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
        val kids = (0 until pageCount).joinToString(" ") { "${pageObj(it)} 0 R" }
        obj(2, "<< /Type /Pages /Kids [$kids] /Count $pageCount >>")
    }

    private fun pageObj(i: Int) = 4 + i * 3

    /** Adds a page showing [jpeg] ([width] x [height] pixels) at [pageWidth] x [pageHeight] points. */
    fun addPage(jpeg: ByteArray, width: Int, height: Int, pageWidth: Float, pageHeight: Float) {
        check(pageIndex < pageCount)
        val p = pageObj(pageIndex)
        val w = fmt(pageWidth)
        val h = fmt(pageHeight)
        obj(p, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 $w $h] /Resources << /XObject << /Im0 ${p + 2} 0 R >> >> /Contents ${p + 1} 0 R >>")
        val content = "q $w 0 0 $h 0 0 cm /Im0 Do Q".toByteArray(Charsets.ISO_8859_1)
        stream(p + 1, "<< /Length ${content.size} >>", content)
        stream(p + 2, "<< /Type /XObject /Subtype /Image /Width $width /Height $height /ColorSpace /DeviceRGB " +
            "/BitsPerComponent 8 /Filter /DCTDecode /Length ${jpeg.size} >>", jpeg)
        pageIndex++
    }

    override fun close() {
        check(pageIndex == pageCount) { "missing pages" }
        val info = 3
        obj(info, "<< /Producer (Homelydrive) /Creator (Homelydrive Scanner) >>")
        // xref needs the objects in order of their number
        val count = 4 + pageCount * 3
        val xref = out.count
        val sb = StringBuilder()
        sb.append("xref\n0 $count\n0000000000 65535 f \n")
        for (n in 1 until count) sb.append(String.format(java.util.Locale.ROOT, "%010d 00000 n \n", offsetOf(n)))
        sb.append("trailer\n<< /Size $count /Root 1 0 R /Info $info 0 R >>\nstartxref\n$xref\n%%EOF\n")
        out.write(sb.toString().toByteArray(Charsets.ISO_8859_1))
        out.close()
    }

    private fun offsetOf(n: Int) = objOffsets[n] ?: error("object $n missing")

    private fun obj(n: Int, body: String) {
        objOffsets[n] = out.count
        out.write("$n 0 obj\n$body\nendobj\n".toByteArray(Charsets.ISO_8859_1))
    }

    private fun stream(n: Int, dict: String, data: ByteArray) {
        objOffsets[n] = out.count
        out.write("$n 0 obj\n$dict\nstream\n".toByteArray(Charsets.ISO_8859_1))
        out.write(data)
        out.write("\nendstream\nendobj\n".toByteArray(Charsets.ISO_8859_1))
    }

    private fun fmt(v: Float) = "%.2f".format(java.util.Locale.ROOT, v)

    private class CountingStream(private val inner: OutputStream) : OutputStream() {
        var count = 0L
            private set

        override fun write(b: Int) {
            inner.write(b)
            count++
        }

        override fun write(b: ByteArray, off: Int, len: Int) {
            inner.write(b, off, len)
            count += len
        }

        override fun flush() = inner.flush()
        override fun close() = inner.close()
    }
}
