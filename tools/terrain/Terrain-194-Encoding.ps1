# The namespace is deliberately stable so repeated dot-sourcing reuses one compiled type.
if ($null -eq ('WrplTerrain194Encoding.Terrain194Encoder' -as [type])) {
    $terrain194CSharp = @'
using System;
using System.IO;
using System.Security.Cryptography;
using System.Text;

namespace WrplTerrain194Encoding
{
    public sealed class Terrain194EncodingResult
    {
        public string SourceSha256 { get; set; }
        public string DataSha256 { get; set; }
        public long DataBytes { get; set; }
        public double MinHeightM { get; set; }
        public double MaxHeightM { get; set; }
        public long SampleCount { get; set; }
    }

    public static class Terrain194Encoder
    {
        private const int MaxBufferBytes = 4 * 1024 * 1024;

        public static Terrain194EncodingResult Convert(
            string source,
            string destination,
            string expectedSourceSha256,
            long expectedBytes)
        {
            if (String.IsNullOrWhiteSpace(source)) throw new ArgumentException("Source is empty.", "source");
            if (String.IsNullOrWhiteSpace(destination)) throw new ArgumentException("Destination is empty.", "destination");
            if (String.IsNullOrWhiteSpace(expectedSourceSha256) || expectedSourceSha256.Length != 64 || !IsHex(expectedSourceSha256))
                throw new ArgumentException("ExpectedSourceSha256 must contain exactly 64 hexadecimal characters.", "expectedSourceSha256");
            if (expectedBytes <= 0 || (expectedBytes & 1L) != 0)
                throw new ArgumentException("ExpectedBytes must be a positive, even byte count.", "expectedBytes");

            string sourceFull = Path.GetFullPath(source);
            string destinationFull = Path.GetFullPath(destination);
            if (String.Equals(sourceFull, destinationFull, StringComparison.OrdinalIgnoreCase))
                throw new IOException("Source and destination must be different files, including case-insensitive path comparisons.");
            if (Directory.Exists(sourceFull))
                throw new IOException("Source is a directory: " + sourceFull);
            if (!File.Exists(sourceFull))
                throw new FileNotFoundException("Source file was not found.", sourceFull);

            string destinationDirectory = Path.GetDirectoryName(destinationFull);
            if (String.IsNullOrEmpty(destinationDirectory) || !Directory.Exists(destinationDirectory))
                throw new DirectoryNotFoundException("Destination directory was not found: " + destinationDirectory);
            if (Directory.Exists(destinationFull))
                throw new IOException("Destination is a directory: " + destinationFull);

            FileInfo sourceInfo = new FileInfo(sourceFull);
            if ((sourceInfo.Length & 1L) != 0)
                throw new InvalidDataException("Source length is odd; int16 terrain requires complete little-endian words.");
            if (sourceInfo.Length != expectedBytes)
                throw new InvalidDataException("Source byte count does not match ExpectedBytes.");

            string expectedHash = expectedSourceSha256.Trim().ToLowerInvariant();
            byte[] expectedDigest = HexToBytes(expectedHash);
            byte[] buffer = new byte[MaxBufferBytes];
            byte[] carryPair = new byte[2];
            long sampleCount = 0;
            short minimum = Int16.MaxValue;
            short maximum = Int16.MinValue;
            byte[] dataDigest;

            using (FileStream input = new FileStream(
                sourceFull,
                FileMode.Open,
                FileAccess.Read,
                FileShare.Read,
                64 * 1024,
                FileOptions.SequentialScan))
            {
                if ((input.Length & 1L) != 0)
                    throw new InvalidDataException("Source length is odd; int16 terrain requires complete little-endian words.");
                if (input.Length != expectedBytes)
                    throw new InvalidDataException("Source byte count does not match ExpectedBytes.");

                string temporaryPath = Path.Combine(
                    destinationDirectory,
                    "wt194-" + Guid.NewGuid().ToString("N") + ".tmp");

                try
                {
                    using (SHA256 sourceHash = SHA256.Create())
                    using (SHA256 dataHash = SHA256.Create())
                    using (FileStream output = new FileStream(
                        temporaryPath,
                        FileMode.CreateNew,
                        FileAccess.Write,
                        FileShare.None,
                        64 * 1024,
                        FileOptions.SequentialScan))
                    {
                        bool hasCarry = false;
                        byte carry = 0;
                        long sourceBytesRead = 0;
                        int read;

                        while ((read = input.Read(buffer, 0, buffer.Length)) > 0)
                        {
                            if (sourceBytesRead + read > expectedBytes)
                                throw new InvalidDataException("Source grew while it was being converted.");

                            sourceHash.TransformBlock(buffer, 0, read, null, 0);
                            sourceBytesRead += read;

                            int start = 0;
                            if (hasCarry)
                            {
                                int rawCarry = (int)carry | ((int)buffer[0] << 8);
                                short sample = unchecked((short)rawCarry);
                                if (sample < minimum) minimum = sample;
                                if (sample > maximum) maximum = sample;
                                sampleCount++;

                                carryPair[0] = carry;
                                carryPair[1] = (byte)(buffer[0] ^ 0x80);
                                dataHash.TransformBlock(carryPair, 0, carryPair.Length, null, 0);
                                output.Write(carryPair, 0, carryPair.Length);
                                hasCarry = false;
                                start = 1;
                            }

                            int completeEnd = start + ((read - start) & ~1);
                            for (int i = start; i < completeEnd; i += 2)
                            {
                                int raw = (int)buffer[i] | ((int)buffer[i + 1] << 8);
                                short sample = unchecked((short)raw);
                                if (sample < minimum) minimum = sample;
                                if (sample > maximum) maximum = sample;
                                sampleCount++;
                                buffer[i + 1] = (byte)(buffer[i + 1] ^ 0x80);
                            }

                            int completeBytes = completeEnd - start;
                            if (completeBytes > 0)
                            {
                                dataHash.TransformBlock(buffer, start, completeBytes, null, 0);
                                output.Write(buffer, start, completeBytes);
                            }

                            if (((read - start) & 1) != 0)
                            {
                                carry = buffer[read - 1];
                                hasCarry = true;
                            }
                        }

                        if (hasCarry)
                            throw new InvalidDataException("Source ended with an incomplete int16 word during conversion.");
                        if (sourceBytesRead != expectedBytes)
                            throw new InvalidDataException("Source byte count changed during conversion.");

                        sourceHash.TransformFinalBlock(new byte[0], 0, 0);
                        byte[] actualSourceDigest = sourceHash.Hash;
                        if (!BytesEqual(actualSourceDigest, expectedDigest))
                            throw new InvalidDataException("Source SHA-256 does not match ExpectedSourceSha256.");

                        dataHash.TransformFinalBlock(new byte[0], 0, 0);
                        dataDigest = dataHash.Hash;
                        output.Flush(true);
                    }

                    VerifyFileHash(temporaryPath, expectedBytes, dataDigest, buffer);
                    VerifyFinalSource(input, expectedBytes, expectedDigest, buffer);
                    Publish(temporaryPath, destinationFull);
                }
                finally
                {
                    try
                    {
                        if (File.Exists(temporaryPath)) File.Delete(temporaryPath);
                    }
                    catch
                    {
                        // Preserve the conversion exception. No recursive cleanup is attempted.
                    }
                }
            }

            Terrain194EncodingResult result = new Terrain194EncodingResult();
            result.SourceSha256 = expectedHash;
            result.DataSha256 = BytesToHex(dataDigest);
            result.DataBytes = expectedBytes;
            result.MinHeightM = (double)minimum;
            result.MaxHeightM = (double)maximum;
            result.SampleCount = sampleCount;
            return result;
        }

        private static void VerifyFinalSource(
            FileStream input,
            long expectedBytes,
            byte[] expectedDigest,
            byte[] buffer)
        {
            input.Position = 0;
            using (SHA256 finalHash = SHA256.Create())
            {
                long total = 0;
                int read;
                while ((read = input.Read(buffer, 0, buffer.Length)) > 0)
                {
                    total += read;
                    if (total > expectedBytes)
                        throw new InvalidDataException("Source grew while it was being converted.");
                    finalHash.TransformBlock(buffer, 0, read, null, 0);
                }
                if (total != expectedBytes || input.Length != expectedBytes)
                    throw new InvalidDataException("Source byte count changed during conversion.");
                finalHash.TransformFinalBlock(new byte[0], 0, 0);
                byte[] actual = finalHash.Hash;
                if (!BytesEqual(actual, expectedDigest))
                    throw new InvalidDataException("Source SHA-256 changed during conversion.");
            }
        }

        private static void VerifyFileHash(
            string path,
            long expectedBytes,
            byte[] expectedDigest,
            byte[] buffer)
        {
            using (FileStream stream = new FileStream(
                path,
                FileMode.Open,
                FileAccess.Read,
                FileShare.Read,
                64 * 1024,
                FileOptions.SequentialScan))
            using (SHA256 hash = SHA256.Create())
            {
                if (stream.Length != expectedBytes)
                    throw new IOException("Temporary output byte count was not preserved.");
                long total = 0;
                int read;
                while ((read = stream.Read(buffer, 0, buffer.Length)) > 0)
                {
                    total += read;
                    hash.TransformBlock(buffer, 0, read, null, 0);
                }
                if (total != expectedBytes)
                    throw new IOException("Temporary output was truncated before publication.");
                hash.TransformFinalBlock(new byte[0], 0, 0);
                byte[] actual = hash.Hash;
                if (!BytesEqual(actual, expectedDigest))
                    throw new IOException("Temporary output SHA-256 verification failed.");
            }
        }

        private static void Publish(string temporaryPath, string destinationPath)
        {
            if (!File.Exists(destinationPath))
            {
                try
                {
                    File.Move(temporaryPath, destinationPath);
                    return;
                }
                catch (IOException)
                {
                    if (!File.Exists(temporaryPath) || !File.Exists(destinationPath)) throw;
                }
            }

            // File.Replace publishes the fully verified file from the same directory atomically.
            File.Replace(temporaryPath, destinationPath, null, true);
        }

        private static bool IsHex(string value)
        {
            for (int i = 0; i < value.Length; i++)
            {
                char c = value[i];
                bool hex = (c >= '0' && c <= '9') ||
                           (c >= 'a' && c <= 'f') ||
                           (c >= 'A' && c <= 'F');
                if (!hex) return false;
            }
            return true;
        }

        private static byte[] HexToBytes(string hex)
        {
            byte[] bytes = new byte[hex.Length / 2];
            for (int i = 0; i < bytes.Length; i++)
                bytes[i] = System.Convert.ToByte(hex.Substring(i * 2, 2), 16);
            return bytes;
        }

        private static string BytesToHex(byte[] bytes)
        {
            StringBuilder builder = new StringBuilder(bytes.Length * 2);
            for (int i = 0; i < bytes.Length; i++)
                builder.Append(bytes[i].ToString("x2", System.Globalization.CultureInfo.InvariantCulture));
            return builder.ToString();
        }

        private static bool BytesEqual(byte[] left, byte[] right)
        {
            if (left == null || right == null || left.Length != right.Length) return false;
            int difference = 0;
            for (int i = 0; i < left.Length; i++) difference |= left[i] ^ right[i];
            return difference == 0;
        }
    }
}
'@

    Add-Type -TypeDefinition $terrain194CSharp -Language CSharp -ErrorAction Stop
    Remove-Variable -Name terrain194CSharp -ErrorAction SilentlyContinue
}

function Convert-WTInt16ToUInt16 {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [ValidateNotNullOrEmpty()]
        [string]$Source,

        [Parameter(Mandatory = $true)]
        [ValidateNotNullOrEmpty()]
        [string]$Destination,

        [Parameter(Mandatory = $true)]
        [ValidateNotNullOrEmpty()]
        [string]$ExpectedSourceSha256,

        [Parameter(Mandatory = $true)]
        [long]$ExpectedBytes
    )

    $ErrorActionPreference = 'Stop'
    $ExpectedSourceSha256 = $ExpectedSourceSha256.Trim()
    if ($ExpectedSourceSha256 -notmatch '^[0-9A-Fa-f]{64}$') {
        throw 'ExpectedSourceSha256 must contain exactly 64 hexadecimal characters.'
    }
    if ($ExpectedBytes -le 0 -or ($ExpectedBytes % 2) -ne 0) {
        throw 'ExpectedBytes must be a positive, even byte count.'
    }

    $converted = [WrplTerrain194Encoding.Terrain194Encoder]::Convert(
        $Source,
        $Destination,
        $ExpectedSourceSha256,
        $ExpectedBytes)

    [pscustomobject][ordered]@{
        source_sha256 = [string]$converted.SourceSha256
        data_sha256 = [string]$converted.DataSha256
        data_bytes = [long]$converted.DataBytes
        min_height_m = [double]$converted.MinHeightM
        max_height_m = [double]$converted.MaxHeightM
        sample_count = [long]$converted.SampleCount
        encoding = 'uint16_le_bias32768'
        altitude_factor = 1
        altitude_offset = -32768
    }
}
