#!/usr/bin/env python


DEFAULT_QUANTILE_VALUES = [75, 90, 93, 94, 95, 96, 97, 98, 99]
DEFAULT_VALUE = [0]


class Multimetrics(object):
    """
    Special aggregator of prehandled quantile data
    Data looks like:
    uploader_timings_request_post_patch-url 0.001 0.001 0.002
    uploader_timings_request_post_upload-from-service
    uploader_timings_request_post_upload-url 0.001 0.002 0.001 0.002 0.001 0.002
    uploader_timings_request_put_patch-target 0.651 0.562 1.171
    """
    def __init__(self, config):
        self.quantile = config.get("values") or DEFAULT_QUANTILE_VALUES
        self.quantile.sort(reverse=True)

    def _parse_metrics(self, line):
        name, _, metrics_as_strings = line.partition(" ")
        print name, _, metrics_as_strings
        if not metrics_as_strings:
            return name, DEFAULT_VALUE
        try:
            mertrics_as_values = map(float, metrics_as_strings.split())
            return name, mertrics_as_values
        except ValueError as err:
            raise Exception("Unable to parse %s: %s" % (line, err))

    def aggregate_host(self, payload, prevtime, currtime):
        """ Convert strings of payload into dict[string][]float and return """
        _parse = self._parse_metrics
        return dict((_parse(line) for line in payload.splitlines()))

    def aggregate_group(self, payload):
        """ Payload is list of dict[string][]float"""
        if len(payload) == 0:
            raise Exception("No data to aggregate")
        names_of_metrics = payload[0].keys()
        result = {}
        for metric in names_of_metrics:
            result[metric] = list()
            all_resuts = list()
            for item in payload:
                all_resuts.extend(item.get(metric, []))

            if len(all_resuts) == 0:
                continue

            all_resuts.sort()
            count = float(len(all_resuts))
            for q in self.quantile:
                index = int(count / 100 * q)
                result[metric].append(all_resuts[index])

        return result

if __name__ == '__main__':
    m = Multimetrics({})
    payload = '''uploader_timings_request_post_patch-url 0.001
uploader_timings_request_post_upload-from-service
uploader_timings_request_post_upload-url 0.001 0.002 0.001 0.002 0.001 0.002
uploader_timings_request_put_patch-target 0.651 0.562 1.171'''
    r = m.aggregate_host(payload, None, None)
    payload = [r, r, r]
    print m.aggregate_group(payload)
