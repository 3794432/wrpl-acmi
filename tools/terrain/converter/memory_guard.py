"""Apply a hard per-worker Windows Job memory budget before loading decoders."""
import ctypes
import os
from ctypes import wintypes as w

LIMIT = 2 * 1024**3


class Basic(ctypes.Structure):
    _fields_ = [('user_time', ctypes.c_int64), ('kernel_time', ctypes.c_int64),
                ('flags', w.DWORD), ('min_ws', ctypes.c_size_t),
                ('max_ws', ctypes.c_size_t), ('processes', w.DWORD),
                ('affinity', ctypes.c_size_t), ('priority', w.DWORD),
                ('scheduling', w.DWORD)]


class IO(ctypes.Structure):
    _fields_ = [(name, ctypes.c_uint64) for name in
                ('read_ops', 'write_ops', 'other_ops', 'read_bytes', 'write_bytes', 'other_bytes')]


class Extended(ctypes.Structure):
    _fields_ = [('basic', Basic), ('io', IO), ('process_limit', ctypes.c_size_t),
                ('job_limit', ctypes.c_size_t), ('peak_process', ctypes.c_size_t),
                ('peak_job', ctypes.c_size_t)]


class MemoryGuard:
    def __init__(self, limit=LIMIT):
        self.limit = limit
        self.handle = None
        if os.name != 'nt':
            import resource
            resource.setrlimit(resource.RLIMIT_AS, (limit, limit))
            return
        k = ctypes.WinDLL('kernel32', use_last_error=True)
        k.CreateJobObjectW.argtypes = (ctypes.c_void_p, w.LPCWSTR)
        k.CreateJobObjectW.restype = w.HANDLE
        k.SetInformationJobObject.argtypes = (w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD)
        k.SetInformationJobObject.restype = w.BOOL
        k.AssignProcessToJobObject.argtypes = (w.HANDLE, w.HANDLE)
        k.AssignProcessToJobObject.restype = w.BOOL
        k.GetCurrentProcess.restype = w.HANDLE
        k.QueryInformationJobObject.argtypes = (w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD, ctypes.c_void_p)
        k.QueryInformationJobObject.restype = w.BOOL
        self.kernel = k
        self.handle = k.CreateJobObjectW(None, None)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        info = Extended()
        info.basic.flags = 0x100 | 0x200 | 0x2000
        info.process_limit = info.job_limit = limit
        if not k.SetInformationJobObject(self.handle, 9, ctypes.byref(info), ctypes.sizeof(info)):
            raise ctypes.WinError(ctypes.get_last_error())
        if not k.AssignProcessToJobObject(self.handle, k.GetCurrentProcess()):
            raise ctypes.WinError(ctypes.get_last_error())
        # Intentionally keep the only Job handle alive for the worker lifetime.

    def stats(self):
        if os.name != 'nt':
            import resource
            return {'limit_bytes': self.limit, 'peak_rss_kib': resource.getrusage(resource.RUSAGE_SELF).ru_maxrss}
        info = Extended()
        if not self.kernel.QueryInformationJobObject(self.handle, 9, ctypes.byref(info), ctypes.sizeof(info), None):
            raise ctypes.WinError(ctypes.get_last_error())
        return {'limit_bytes': self.limit, 'peak_commit_bytes': info.peak_process,
                'peak_job_commit_bytes': info.peak_job}
